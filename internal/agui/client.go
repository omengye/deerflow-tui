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
	"net/url"
	"strings"
	"time"
)

type Stream struct {
	RunID  string
	Events <-chan EventEnvelope
	Errs   <-chan error
	cancel context.CancelFunc
}

type RunRequest struct {
	RunID       string
	ThreadID    string
	History     []ChatMessage
	Resume      []ResumeEntry
	LastEventID string
}

type HTTPError struct {
	StatusCode int
	Body       string
}

type threadDetailResponse struct {
	Messages []map[string]any `json:"messages"`
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("run request failed: status=%d body=%s", e.StatusCode, e.Body)
}

func (e *HTTPError) Retryable() bool {
	return e.StatusCode == http.StatusRequestTimeout || e.StatusCode == http.StatusTooEarly || e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

func (s *Stream) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

type Client struct {
	endpoint      string
	cancelBaseURL string
	headers       map[string]string
	initialState  map[string]any
	httpClient    *http.Client
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
		endpoint:      endpoint,
		cancelBaseURL: deriveCancelBaseURL(endpoint),
		headers:       clonedHeaders,
		initialState:  clonedState,
		httpClient:    &http.Client{Timeout: 0},
	}
}

func deriveCancelBaseURL(endpoint string) string {
	// Strip the AG-UI path suffix to get the API base.
	// "http://host:8000/api/chat/agui" → "http://host:8000/api"
	if idx := strings.LastIndex(endpoint, "/chat/agui"); idx >= 0 {
		return endpoint[:idx]
	}
	// "http://host:8000/agent" → "http://host:8000/api"
	if idx := strings.LastIndex(endpoint, "/"); idx > len("http://") {
		return endpoint[:idx] + "/api"
	}
	return endpoint + "/api"
}

func (c *Client) StartRun(ctx context.Context, threadID string, history []ChatMessage) (*Stream, error) {
	return c.ResumeRun(ctx, threadID, history, nil)
}

func (c *Client) ResumeRun(ctx context.Context, threadID string, history []ChatMessage, resume []ResumeEntry) (*Stream, error) {
	runID, err := NewRunID()
	if err != nil {
		return nil, fmt.Errorf("create run id: %w", err)
	}
	return c.ConnectRun(ctx, RunRequest{RunID: runID, ThreadID: threadID, History: history, Resume: resume})
}

func (c *Client) ConnectRun(ctx context.Context, run RunRequest) (*Stream, error) {
	payload := RunAgentInput{
		RunID:             run.RunID,
		ThreadID:          run.ThreadID,
		State:             c.initialState,
		Messages:          NormalizeMessages(run.History),
		OnDisconnect:      "continue",
		MultitaskStrategy: "interrupt",
	}
	if len(run.Resume) > 0 {
		payload.Resume = run.Resume
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
	if strings.TrimSpace(run.LastEventID) != "" {
		req.Header.Set("Last-Event-ID", strings.TrimSpace(run.LastEventID))
	}
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
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}

	events := make(chan EventEnvelope)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)
		defer resp.Body.Close()

		if err := parseSSE(resp.Body, func(eventID, data string) {
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
			env.SSEID = eventID

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

	return &Stream{RunID: run.RunID, Events: events, Errs: errs, cancel: cancel}, nil
}

func (c *Client) CancelRun(ctx context.Context, runID string) error {
	url := fmt.Sprintf("%s/runs/%s/cancel", c.cancelBaseURL, runID)
	body := strings.NewReader(`{"action":"interrupt"}`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("create cancel request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cancel run request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return fmt.Errorf("cancel run failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (c *Client) GetThreadMessages(ctx context.Context, threadID string) ([]ChatMessage, error) {
	requestURL := fmt.Sprintf("%s/threads/%s", c.cancelBaseURL, url.PathEscape(threadID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create thread request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("thread request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}

	var detail threadDetailResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decode thread response: %w", err)
	}
	rawMessages := make([]any, 0, len(detail.Messages))
	for _, message := range detail.Messages {
		if _, ok := message["role"]; !ok {
			if typ, ok := message["type"].(string); ok {
				message["role"] = NormalizeRole(typ)
			}
		}
		rawMessages = append(rawMessages, message)
	}
	return MessagesFromSnapshot(rawMessages), nil
}

func parseSSE(r io.Reader, onData func(eventID, data string)) error {
	reader := bufio.NewReader(r)
	var dataLines []string
	var eventID string

	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		onData(eventID, strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		eventID = ""
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
		} else if id, ok := strings.CutPrefix(trimmed, "id:"); ok {
			eventID = strings.TrimSpace(id)
		}

		if err == io.EOF {
			flush()
			return nil
		}
	}
}

func NewRunID() (string, error) {
	return newID("run")
}

func newID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), hex.EncodeToString(buf)), nil
}
