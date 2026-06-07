package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const DefaultEndpoint = "http://localhost:8000/agent"

type Config struct {
	Endpoint     string
	Headers      map[string]string
	InitialState map[string]any
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		Endpoint: strings.TrimSpace(os.Getenv("AG_UI_ENDPOINT")),
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}

	headers, err := parseHeaders(os.Getenv("AG_UI_HEADERS"))
	if err != nil {
		return Config{}, err
	}
	initial, err := parseInitialState(os.Getenv("AG_UI_INITIAL_STATE"))
	if err != nil {
		return Config{}, err
	}

	cfg.Headers = headers
	cfg.InitialState = initial
	return cfg, nil
}

func parseHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("AG_UI_HEADERS must be a JSON object of string values: %w", err)
	}

	headers := make(map[string]string, len(parsed))
	for k, v := range parsed {
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("AG_UI_HEADERS must be a JSON object of string values")
		}
		headers[k] = s
	}

	return headers, nil
}

func parseInitialState(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("AG_UI_INITIAL_STATE must be a JSON object: %w", err)
	}

	if parsed == nil {
		return map[string]any{}, nil
	}

	return parsed, nil
}
