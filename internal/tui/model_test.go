package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deerflow-tui/internal/agui"
	"deerflow-tui/internal/config"
)

func newTestModel() model {
	m := NewModel(config.Config{Endpoint: config.DefaultEndpoint}).(model)
	m.width = 100
	m.height = 30
	m.layout()
	return m
}

func TestTextMessageStreamsIntoSingleBlockAndHistory(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "m1"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "m1", Delta: "hel", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m1", "delta": "hel"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "m1", Delta: "lo", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m1", "delta": "lo"}})

	if got := m.renderBlocks(); !strings.Contains(got, "hello") || strings.Count(got, "[TEXT_MESSAGE]") != 1 {
		t.Fatalf("streamed block not updated in place:\n%s", got)
	}

	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_END", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_END", "messageId": "m1"}})
	if len(m.history) != 1 || m.history[0].Role != agui.RoleAssistant || m.history[0].Content != "hello" {
		t.Fatalf("unexpected history: %#v", m.history)
	}
}

func TestTextMessageShowsAgentNameFromRawEvent(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "m1"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m1", "raw_event": map[string]any{"name": "researcher"}}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "m1", Delta: "hello", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m1", "delta": "hello"}})

	if got := m.renderBlocks(); !strings.Contains(got, "[TEXT_MESSAGE] #m1 agent: researcher") || !strings.Contains(got, "hello") {
		t.Fatalf("agent name not rendered in text message block:\n%s", got)
	}

	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_END", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_END", "messageId": "m1"}})
	if len(m.history) != 1 || m.history[0].Name != "researcher" {
		t.Fatalf("agent name not recorded in history: %#v", m.history)
	}
}

func TestReplayFilterDropsHistoricalTextByID(t *testing.T) {
	m := newTestModel()
	m.history = []agui.ChatMessage{{Role: agui.RoleAssistant, ID: "m1", Content: "already shown"}}
	m.beginRun(m.history)
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "m1"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "m1", Delta: "already shown", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m1", "delta": "already shown"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_END", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_END", "messageId": "m1"}})

	if got := m.renderBlocks(); strings.Contains(got, "already shown") {
		t.Fatalf("historical text leaked into view:\n%s", got)
	}
}

func TestReplayFilterStripsHistoricalPrefix(t *testing.T) {
	m := newTestModel()
	m.history = []agui.ChatMessage{{Role: agui.RoleAssistant, ID: "old", Content: "old answer"}}
	m.beginRun(m.history)
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: "new", Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "new"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "new", Delta: "old answer\nnew tail", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "new", "delta": "old answer\nnew tail"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_END", MessageID: "new", Raw: map[string]any{"type": "TEXT_MESSAGE_END", "messageId": "new"}})

	got := m.renderBlocks()
	if strings.Contains(got, "old answer") || !strings.Contains(got, "new tail") {
		t.Fatalf("historical prefix not stripped correctly:\n%s", got)
	}
}

func TestThinkingTextMessageEventsRenderThinkingBlock(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agui.EventEnvelope{Type: "THINKING_TEXT_MESSAGE_START", Raw: map[string]any{"type": "THINKING_TEXT_MESSAGE_START"}})
	m.handleEvent(agui.EventEnvelope{Type: "THINKING_TEXT_MESSAGE_CONTENT", Delta: "plan", Raw: map[string]any{"type": "THINKING_TEXT_MESSAGE_CONTENT", "delta": "plan"}})
	m.handleEvent(agui.EventEnvelope{Type: "THINKING_TEXT_MESSAGE_END", Raw: map[string]any{"type": "THINKING_TEXT_MESSAGE_END"}})

	if got := m.renderBlocks(); !strings.Contains(got, "[THINKING]") || !strings.Contains(got, "plan") {
		t.Fatalf("thinking block missing:\n%s", got)
	}
}

func TestStaleRunMessagesAreIgnored(t *testing.T) {
	m := newTestModel()
	m.runSeq = 2
	updated, _ := m.Update(aguiEventMsg{runSeq: 1, event: agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "stale", Delta: "bad", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT"}}})
	m = updated.(model)
	if got := m.renderBlocks(); strings.Contains(got, "bad") {
		t.Fatalf("stale event was rendered:\n%s", got)
	}
}

func TestInterruptResumeEntriesValidateAndRender(t *testing.T) {
	m := newTestModel()
	expiresAt := time.Now().Add(time.Hour).Format(time.RFC3339)
	m.handleEvent(agui.EventEnvelope{
		Type: "RUN_FINISHED",
		Raw: map[string]any{
			"type": "RUN_FINISHED",
			"outcome": map[string]any{
				"type": "interrupt",
				"interrupts": []any{map[string]any{
					"id":        "i1",
					"reason":    "input_required",
					"message":   "Need approval",
					"expiresAt": expiresAt,
				}},
			},
		},
	})

	if len(m.interrupts) != 1 || m.status != "Requires interrupt response" {
		t.Fatalf("interrupt not captured: status=%q interrupts=%#v", m.status, m.interrupts)
	}
	entries, err := m.buildResumeEntries("approved")
	if err != nil {
		t.Fatalf("buildResumeEntries returned error: %v", err)
	}
	if len(entries) != 1 || entries[0].InterruptID != "i1" || entries[0].Payload != "approved" {
		t.Fatalf("unexpected resume entries: %#v", entries)
	}
	if got := m.renderBlocks(); !strings.Contains(got, "[INTERRUPT]") || !strings.Contains(got, "Need approval") {
		t.Fatalf("interrupt block missing:\n%s", got)
	}
}

func TestValidateResumeEntriesRejectsMissingDuplicateAndExpired(t *testing.T) {
	interrupts := []agui.Interrupt{
		{ID: "i1", Reason: "input_required", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)},
		{ID: "i2", Reason: "confirmation"},
	}
	if _, err := validateResumeEntries(interrupts, []agui.ResumeEntry{{InterruptID: "i1", Status: "resolved"}}); err == nil {
		t.Fatal("expected missing response error")
	}
	if _, err := validateResumeEntries(interrupts, []agui.ResumeEntry{{InterruptID: "i1", Status: "resolved"}, {InterruptID: "i1", Status: "cancelled"}, {InterruptID: "i2", Status: "resolved"}}); err == nil {
		t.Fatal("expected duplicate response error")
	}
	expired := []agui.Interrupt{{ID: "i1", Reason: "input_required", ExpiresAt: time.Now().Add(-time.Hour).Format(time.RFC3339)}}
	if _, err := validateResumeEntries(expired, []agui.ResumeEntry{{InterruptID: "i1", Status: "resolved"}}); err == nil {
		t.Fatal("expected expired interrupt error")
	}
}

func TestToolCallLifecycleRecordsAssistantToolCallAndResult(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_START", ToolCallID: "tc1", ToolCallName: "search", Raw: map[string]any{"type": "TOOL_CALL_START", "toolCallId": "tc1", "toolCallName": "search"}})
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_ARGS", ToolCallID: "tc1", Delta: `{"q":"x"}`, Raw: map[string]any{"type": "TOOL_CALL_ARGS", "toolCallId": "tc1", "delta": `{"q":"x"}`}})
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_RESULT", ToolCallID: "tc1", Content: `{"ok":true}`, Raw: map[string]any{"type": "TOOL_CALL_RESULT", "toolCallId": "tc1", "content": `{"ok":true}`, "role": "tool"}})

	if len(m.history) != 2 {
		t.Fatalf("expected assistant tool call + tool result in history, got %#v", m.history)
	}
	if len(m.history[0].ToolCalls) != 1 || m.history[0].ToolCalls[0].Function.Name != "search" || m.history[0].ToolCalls[0].Function.Arguments != `{"q":"x"}` {
		t.Fatalf("unexpected assistant tool call history: %#v", m.history[0])
	}
	if m.history[1].Role != agui.RoleTool || m.history[1].ToolCallID != "tc1" {
		t.Fatalf("unexpected tool result history: %#v", m.history[1])
	}
	if got := m.renderBlocks(); !strings.Contains(got, "search | args") || !strings.Contains(got, `result: {"ok":true}`) {
		t.Fatalf("tool block missing lifecycle details:\n%s", got)
	}
}

func TestMessagesSnapshotImportsHistory(t *testing.T) {
	m := newTestModel()
	raw := []any{
		map[string]any{"id": "u1", "role": "user", "content": "hello"},
		map[string]any{"id": "a1", "role": "assistant", "content": "hi", "toolCalls": []any{map[string]any{"id": "tc1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"x":1}`}}}},
		map[string]any{"id": "t1", "role": "tool", "toolCallId": "tc1", "content": `{"result":1}`},
	}
	m.handleEvent(agui.EventEnvelope{Type: "MESSAGES_SNAPSHOT", Raw: map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": raw}})

	if len(m.history) != 2 {
		b, _ := json.Marshal(m.history)
		t.Fatalf("expected user + assistant history after tool result attachment, got %s", b)
	}
	got := m.renderBlocks()
	if !strings.Contains(got, "hello") || !strings.Contains(got, "lookup") {
		t.Fatalf("snapshot not rendered correctly:\n%s", got)
	}
	if strings.Contains(got, "MESSAGES_SNAPSHOT") || strings.Contains(got, "imported 2 messages") || strings.Contains(got, "[SYSTEM]") {
		t.Fatalf("snapshot event should not be rendered:\n%s", got)
	}
}
