package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"deerflow-tui/internal/agui"
	"deerflow-tui/internal/config"
)

func TestRunSessionStoreRoundTripAndClear(t *testing.T) {
	store := newRunSessionStore(t.TempDir())
	want := &runSession{
		Endpoint:    "http://localhost:8000/api/chat/agui",
		ThreadID:    "thread-1",
		RunID:       "run-1",
		LastEventID: "10-2",
		UpdatedAt:   time.Now(),
	}
	if err := store.save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.load(want.Endpoint)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.RunID != want.RunID || got.LastEventID != want.LastEventID {
		t.Fatalf("unexpected session: %#v", got)
	}
	if err := store.clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = store.load(want.Endpoint)
	if err != nil || got != nil {
		t.Fatalf("expected cleared session, got %#v err=%v", got, err)
	}
}

func TestNewModelLoadsPersistedRunSession(t *testing.T) {
	stateDir := t.TempDir()
	store := newRunSessionStore(stateDir)
	endpoint := "http://localhost:8000/api/chat/agui"
	if err := store.save(&runSession{Endpoint: endpoint, ThreadID: "thread-1", RunID: "run-1", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("save: %v", err)
	}

	m := NewModel(config.Config{Endpoint: endpoint, StateDir: stateDir}).(model)
	if m.runSession == nil || m.activeRunID != "run-1" || m.threadID != "thread-1" {
		t.Fatalf("persisted session not restored: %#v", m.runSession)
	}
	if !m.running || m.status != "Restoring agent stream..." {
		t.Fatalf("unexpected recovery state: running=%v status=%q", m.running, m.status)
	}
}

func TestReconnectAdvancesConnectionSequence(t *testing.T) {
	m := newTestModel()
	m.setRunSession(&runSession{Endpoint: m.cfg.Endpoint, ThreadID: m.threadID, RunID: "run-1", UpdatedAt: time.Now()})
	m.running = true
	m.runSeq = 7

	cmd, handled := m.handleReconnectableError(errors.New("connection reset"))
	if !handled || cmd == nil {
		t.Fatal("expected reconnect command")
	}
	if m.runSeq != 8 || !m.reconnectScheduled {
		t.Fatalf("expected new connection sequence, runSeq=%d scheduled=%v", m.runSeq, m.reconnectScheduled)
	}

	updated, _ := m.Update(streamDoneMsg{runSeq: 7})
	m = updated.(model)
	if !m.running || !m.reconnectScheduled {
		t.Fatal("stale stream completion interrupted the reconnect")
	}
}

func TestReconnectDelayIsCapped(t *testing.T) {
	if got := reconnectDelay(1); got != time.Second {
		t.Fatalf("first delay = %v", got)
	}
	if got := reconnectDelay(20); got != 30*time.Second {
		t.Fatalf("capped delay = %v", got)
	}
}

func TestReplayExpiredFetchesLatestThreadHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"thread_id":"thread-1","messages":[{"type":"ai","content":"final answer","id":"a1"}],"artifacts":[],"status":"idle"}`))
	}))
	defer srv.Close()

	m := newTestModel()
	m.cfg.Endpoint = srv.URL + "/api/chat/agui"
	m.client = agui.NewClient(m.cfg.Endpoint, nil, nil)
	m.threadID = "thread-1"
	m.setRunSession(&runSession{Endpoint: m.cfg.Endpoint, ThreadID: m.threadID, RunID: "run-1", UpdatedAt: time.Now()})

	cmd, handled := m.handleReconnectableError(&agui.HTTPError{StatusCode: http.StatusGone, Body: "expired"})
	if !handled || cmd == nil || m.runSession != nil {
		t.Fatalf("expected handled 410 with cleared session: handled=%v session=%#v", handled, m.runSession)
	}
	msg := runCmd(t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(model)
	if len(m.history) != 1 || m.history[0].Role != agui.RoleAssistant || m.history[0].Content != "final answer" {
		t.Fatalf("thread history not recovered: %#v", m.history)
	}
	if m.status != "Idle" {
		t.Fatalf("expected Idle after recovery, got %q", m.status)
	}
}

func TestRunSessionStoreUsesExpectedFilename(t *testing.T) {
	dir := t.TempDir()
	store := newRunSessionStore(dir)
	if store.path != filepath.Join(dir, "active-run.json") {
		t.Fatalf("unexpected path %q", store.path)
	}
}
