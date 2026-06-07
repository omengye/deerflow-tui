package agui

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Stream struct {
	Events <-chan EventEnvelope
	Errs   <-chan error
	cancel context.CancelFunc
}

func (s *Stream) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

type Client struct {
	endpoint     string
	headers      map[string]string
	initialState map[string]any
	httpClient   *http.Client
}

func NewClient(endpoint string, headers map[string]string, initialState map[string]any) *Client {
	clonedHeaders := map[string]string{}
	for k, v := range headers {
		clonedHeaders[k] = v
	}
	clonedState := map[string]any{}
	for k, v := range initialState {
		clonedState[k] = v
	}

	return &Client{
		endpoint:     endpoint,
		headers:      clonedHeaders,
		initialState: clonedState,
		httpClient:   &http.Client{Timeout: 0},
	}
}

func (c *Client) StartRun(ctx context.Context, threadID string, history []ChatMessage) (*Stream, error) {
	return c.ResumeRun(ctx, threadID, history, nil)
}

func (c *Client) ResumeRun(ctx context.Context, threadID string, history []ChatMessage, resume []ResumeEntry) (*Stream, error) {
	runID, err := newID("run")
	if err != nil {
		return nil, fmt.Errorf("create run id: %w", err)
	}

	payload := RunAgentInput{
		RunID:    runID,
		ThreadID: threadID,
		State:    c.initialState,
		Messages: NormalizeMessages(history),
	}
	if len(resume) > 0 {
		payload.Resume = resume
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal run payload: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("run request failed: %w", err)
	}

	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		cancel()
		return nil, fmt.Errorf("run request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	events := make(chan EventEnvelope)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)
		defer resp.Body.Close()

		if err := parseSSE(resp.Body, func(data string) {
			if strings.TrimSpace(data) == "" {
				return
			}

			env, err := ParseEventEnvelope([]byte(data))
			if err != nil {
				env = EventEnvelope{
					Type: "RAW",
					Raw: map[string]any{
						"type":   "RAW",
						"event":  data,
						"source": "non-json-sse",
					},
				}
			}

			select {
			case <-ctx.Done():
				return
			case events <- env:
			}
		}); err != nil {
			select {
			case <-ctx.Done():
				return
			case errs <- err:
			}
		}
	}()

	return &Stream{Events: events, Errs: errs, cancel: cancel}, nil
}

func parseSSE(r io.Reader, onData func(data string)) error {
	reader := bufio.NewReader(r)
	var dataLines []string

	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		onData(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("read sse: %w", err)
		}

		trimmed := strings.TrimRight(line, "\r\n")

		if trimmed == "" {
			flush()
		} else if data, ok := strings.CutPrefix(trimmed, "data:"); ok {
			data = strings.TrimSpace(data)
			dataLines = append(dataLines, data)
		}

		if err == io.EOF {
			flush()
			return nil
		}
	}
}

func newID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), hex.EncodeToString(buf)), nil
}
