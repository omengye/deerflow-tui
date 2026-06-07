package agui

import (
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
