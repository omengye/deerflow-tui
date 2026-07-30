package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	reconnectWindow    = 10 * time.Minute
	sessionMetadataTTL = time.Hour
	initialReplayID    = "0-0"
)

type runSession struct {
	Endpoint    string    `json:"endpoint"`
	ThreadID    string    `json:"thread_id"`
	RunID       string    `json:"run_id"`
	LastEventID string    `json:"last_event_id,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type runSessionStore struct {
	path string
}

func newRunSessionStore(stateDir string) *runSessionStore {
	if stateDir == "" {
		return nil
	}
	return &runSessionStore{path: filepath.Join(stateDir, "active-run.json")}
}

func (s *runSessionStore) load(endpoint string) (*runSession, error) {
	if s == nil {
		return nil, nil
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var session runSession
	if err := json.Unmarshal(raw, &session); err != nil {
		_ = s.clear()
		return nil, err
	}
	if session.Endpoint != endpoint || session.ThreadID == "" || session.RunID == "" || time.Since(session.UpdatedAt) > sessionMetadataTTL {
		_ = s.clear()
		return nil, nil
	}
	return &session, nil
}

func (s *runSessionStore) save(session *runSession) error {
	if s == nil || session == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.path); err != nil {
		if writeErr := os.WriteFile(s.path, raw, 0o600); writeErr != nil {
			_ = os.Remove(temporary)
			return writeErr
		}
		_ = os.Remove(temporary)
	}
	return nil
}

func (s *runSessionStore) clear() error {
	if s == nil {
		return nil
	}
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func reconnectDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second << min(attempt-1, 5)
	return min(delay, 30*time.Second)
}
