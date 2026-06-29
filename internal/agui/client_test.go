package agui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSSECollectsMultilineData(t *testing.T) {
	var events []string
	err := parseSSE(strings.NewReader("event: message\ndata: {\"type\":\"TEXT_MESSAGE_CONTENT\",\ndata: \"delta\":\"hi\"}\n\n"), func(data string) {
		events = append(events, data)
	})
	if err != nil {
		t.Fatalf("parseSSE returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if got, want := events[0], "{\"type\":\"TEXT_MESSAGE_CONTENT\",\n\"delta\":\"hi\"}"; got != want {
		t.Fatalf("unexpected data:\n got: %q\nwant: %q", got, want)
	}
}

func TestParseSSEFlushesTrailingEventWithoutBlankLine(t *testing.T) {
	var events []string
	err := parseSSE(strings.NewReader("data: {\"type\":\"RUN_FINISHED\"}"), func(data string) {
		events = append(events, data)
	})
	if err != nil {
		t.Fatalf("parseSSE returned error: %v", err)
	}
	if len(events) != 1 || events[0] != "{\"type\":\"RUN_FINISHED\"}" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestDeriveCancelBaseURL(t *testing.T) {
	cases := []struct {
		endpoint string
		want     string
	}{
		{"http://localhost:8000/api/chat/agui", "http://localhost:8000/api"},
		{"http://example.com:8080/api/chat/agui", "http://example.com:8080/api"},
		{"https://agent.example.com/api/chat/agui", "https://agent.example.com/api"},
		{"http://localhost:8000/agent", "http://localhost:8000/api"},
		{"http://localhost:8000", "http://localhost:8000/api"},
	}
	for _, tc := range cases {
		got := deriveCancelBaseURL(tc.endpoint)
		if got != tc.want {
			t.Errorf("deriveCancelBaseURL(%q) = %q, want %q", tc.endpoint, got, tc.want)
		}
	}
}

func TestCancelRunSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/runs/test-run-1/cancel") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true,"run":{"run_id":"test-run-1","status":"interrupted"}}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL+"/chat/agui", nil, nil)
	err := client.CancelRun(context.Background(), "test-run-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCancelRunNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"detail":"Run test-run-1 not found"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL+"/chat/agui", nil, nil)
	err := client.CancelRun(context.Background(), "test-run-1")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "status=404") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamRunIDPropagated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"RUN_STARTED\",\"runId\":\"srv-run-1\"}\n\n"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL+"/chat/agui", nil, nil)
	stream, err := client.StartRun(context.Background(), "thread-1", nil)
	if err != nil {
		t.Fatalf("StartRun failed: %v", err)
	}
	defer stream.Close()

	if stream.RunID == "" {
		t.Fatal("expected RunID to be set on Stream")
	}
}
