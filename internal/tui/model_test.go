package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

	if got := m.renderBlocks(); !strings.Contains(got, "hello") || strings.Count(got, "[Assistant]") != 1 || strings.Contains(got, "#m1") || strings.Contains(got, "[TEXT_MESSAGE]") {
		t.Fatalf("streamed block not updated in place:\n%s", got)
	}

	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_END", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_END", "messageId": "m1"}})
	if len(m.history) != 1 || m.history[0].Role != agui.RoleAssistant || m.history[0].Content != "hello" {
		t.Fatalf("unexpected history: %#v", m.history)
	}
}

func TestAssistantMarkdownIsRendered(t *testing.T) {
	m := newTestModel()
	markdown := "### Beijing weather\n\n**bold text**\n\n| Name | Value |\n| --- | --- |\n| alpha | 1 |"
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: "md1", Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "md1"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "md1", Delta: markdown, Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "md1", "delta": markdown}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_END", MessageID: "md1", Raw: map[string]any{"type": "TEXT_MESSAGE_END", "messageId": "md1"}})

	got := m.renderBlocks()
	plain := ansi.Strip(got)
	if strings.Contains(plain, "### Beijing weather") || strings.Contains(plain, "**bold text**") || strings.Contains(plain, "| --- | --- |") {
		t.Fatalf("markdown syntax was displayed instead of rendered:\n%s", got)
	}
	if !strings.Contains(plain, "Beijing weather") || !strings.Contains(plain, "bold text") || !strings.Contains(plain, "alpha") || !strings.Contains(plain, "Value") {
		t.Fatalf("rendered markdown content is incomplete:\n%s", got)
	}
	if len(m.history) != 1 || m.history[0].Content != markdown {
		t.Fatalf("raw markdown must remain in history: %#v", m.history)
	}
	for _, line := range strings.Split(got, "\n") {
		if width := lipgloss.Width(line); width > m.viewport.Width {
			t.Fatalf("rendered markdown exceeded viewport width: width=%d limit=%d line=%q", width, m.viewport.Width, line)
		}
	}
}

func TestTextMessageShowsAgentNameFromRawEvent(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "m1"}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "m1", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m1", "raw_event": map[string]any{"name": "researcher"}}})
	m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "m1", Delta: "hello", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m1", "delta": "hello"}})

	if got := m.renderBlocks(); !strings.Contains(got, "[Assistant (researcher)]") || strings.Contains(got, "#m1") || strings.Contains(got, "[TEXT_MESSAGE]") || !strings.Contains(got, "hello") {
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
	plain := ansi.Strip(got)
	if strings.Contains(plain, "old answer") || !strings.Contains(plain, "new tail") {
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

func TestStaleAsyncMessageKeepsCurrentStreamWaitAlive(t *testing.T) {
	errTestStream := errors.New("stale stream error")
	cases := map[string]tea.Msg{
		"event": aguiEventMsg{runSeq: 1, event: agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: "stale", Delta: "bad", Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT"}}},
		"error": aguiErrorMsg{runSeq: 1, err: errTestStream},
		"done":  streamDoneMsg{runSeq: 1},
	}

	for name, stale := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestModel()
			m.runSeq = 2
			m.running = true
			m.stream = &agui.Stream{}
			m.asyncCh <- aguiEventMsg{runSeq: 2, event: agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: "current", Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "current"}}}

			updated, cmd := m.Update(stale)
			m = updated.(model)

			if cmd == nil {
				t.Fatal("expected stale async message to keep waiting on the active stream")
			}
			got := runCmd(t, cmd)
			current, ok := got.(aguiEventMsg)
			if !ok || current.runSeq != 2 || current.event.MessageID != "current" {
				t.Fatalf("expected next current stream message, got %#v", got)
			}
			if rendered := m.renderBlocks(); strings.Contains(rendered, "bad") {
				t.Fatalf("stale event was rendered:\n%s", rendered)
			}
		})
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
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_END", ToolCallID: "tc1", Raw: map[string]any{"type": "TOOL_CALL_END", "toolCallId": "tc1"}})
	if got := m.renderBlocks(); !strings.Contains(got, "search") || strings.Contains(got, `{"q":"x"}`) || !m.hasRunningTools() {
		t.Fatalf("running tool block should show only its animation and name:\n%s", got)
	}
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
	if got := m.renderBlocks(); !strings.Contains(got, "✓ search") || strings.Contains(got, `{"q":"x"}`) || strings.Contains(got, `{"ok":true}`) || strings.Contains(got, "args:") || strings.Contains(got, "result:") {
		t.Fatalf("tool block should show only its completed status and name:\n%s", got)
	}
}

func TestToolCallSpinnerAdvancesWhileRunning(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_START", ToolCallID: "tc-spinner", ToolCallName: "search", Raw: map[string]any{"type": "TOOL_CALL_START", "toolCallId": "tc-spinner", "toolCallName": "search"}})
	before := m.renderBlocks()

	updated, _ := m.Update(m.spinner.Tick())
	m = updated.(model)
	after := m.renderBlocks()
	if before == after {
		t.Fatalf("tool spinner did not advance:\n%s", after)
	}
}

func TestToolCallErrorShowsFailureWithoutDetails(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_START", ToolCallID: "tc-error", ToolCallName: "fetch", Raw: map[string]any{"type": "TOOL_CALL_START", "toolCallId": "tc-error", "toolCallName": "fetch"}})
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_ARGS", ToolCallID: "tc-error", Delta: `{"url":"https://example.com/private"}`, Raw: map[string]any{"type": "TOOL_CALL_ARGS", "toolCallId": "tc-error"}})
	m.handleEvent(agui.EventEnvelope{Type: "TOOL_CALL_RESULT", ToolCallID: "tc-error", Content: "sensitive failure detail", Raw: map[string]any{"type": "TOOL_CALL_RESULT", "toolCallId": "tc-error", "content": "sensitive failure detail", "isError": true, "role": "tool"}})

	got := m.renderBlocks()
	if !strings.Contains(got, "✗ fetch") || strings.Contains(got, "example.com") || strings.Contains(got, "sensitive failure detail") {
		t.Fatalf("failed tool call leaked arguments or result:\n%s", got)
	}
}

func TestRunFinishedDoesNotDumpPayload(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.handleEvent(agui.EventEnvelope{Type: "RUN_FINISHED", Raw: map[string]any{"type": "RUN_FINISHED", "runId": "run-1", "result": "sensitive tool output"}})

	if got := m.renderBlocks(); strings.Contains(got, "sensitive tool output") || strings.Contains(got, `"result"`) {
		t.Fatalf("run completion leaked its raw payload:\n%s", got)
	}
}

func TestMessagesSnapshotImportsHistory(t *testing.T) {
	m := newTestModel()
	raw := []any{
		map[string]any{"id": "u1", "role": "user", "content": "hello"},
		map[string]any{"id": "a1", "role": "assistant", "content": "**hi**", "toolCalls": []any{map[string]any{"id": "tc1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"x":1}`}}}},
		map[string]any{"id": "t1", "role": "tool", "toolCallId": "tc1", "content": `{"result":1}`},
	}
	m.handleEvent(agui.EventEnvelope{Type: "MESSAGES_SNAPSHOT", Raw: map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": raw}})

	if len(m.history) != 3 {
		b, _ := json.Marshal(m.history)
		t.Fatalf("expected user + assistant + tool history, got %s", b)
	}
	if m.history[2].Role != agui.RoleTool || m.history[2].ToolCallID != "tc1" {
		t.Fatalf("tool result missing from imported history: %#v", m.history)
	}
	got := m.renderBlocks()
	if !strings.Contains(got, "hello") || !strings.Contains(got, "✓ lookup") {
		t.Fatalf("snapshot not rendered correctly:\n%s", got)
	}
	if strings.Contains(got, `{"x":1}`) || strings.Contains(got, `{"result":1}`) || strings.Contains(got, "args:") || strings.Contains(got, "result:") || strings.Contains(got, "[TOOL]") {
		t.Fatalf("snapshot leaked tool arguments or results:\n%s", got)
	}
	if strings.Contains(got, "**hi**") {
		t.Fatalf("snapshot assistant markdown was not rendered:\n%s", got)
	}
	if !strings.Contains(got, "[You]") || !strings.Contains(got, "[Assistant]") || strings.Contains(got, "#u1") || strings.Contains(got, "#a1") {
		t.Fatalf("snapshot message headers should not expose message ids:\n%s", got)
	}
	if strings.Contains(got, "MESSAGES_SNAPSHOT") || strings.Contains(got, "imported 2 messages") || strings.Contains(got, "[SYSTEM]") {
		t.Fatalf("snapshot event should not be rendered:\n%s", got)
	}
}

func TestSnapshotAndStreamDoNotDuplicateAssistantMessage(t *testing.T) {
	for name, streamID := range map[string]string{
		"same id":      "lc_run--snapshot",
		"different id": "lc_run--stream",
	} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel()
			answer := "duplicate answer"
			m.handleEvent(agui.EventEnvelope{Type: "MESSAGES_SNAPSHOT", Raw: map[string]any{
				"type": "MESSAGES_SNAPSHOT",
				"messages": []any{map[string]any{
					"id": "lc_run--snapshot", "role": "assistant", "content": answer,
				}},
			}})
			m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_START", MessageID: streamID, Raw: map[string]any{"type": "TEXT_MESSAGE_START", "messageId": streamID}})
			m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_CONTENT", MessageID: streamID, Delta: answer, Raw: map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": streamID, "delta": answer}})

			streaming := ansi.Strip(m.renderBlocks())
			if count := strings.Count(streaming, answer); count != 1 {
				t.Fatalf("assistant answer rendered %d times before TEXT_MESSAGE_END instead of once:\n%s", count, streaming)
			}
			m.handleEvent(agui.EventEnvelope{Type: "TEXT_MESSAGE_END", MessageID: streamID, Raw: map[string]any{"type": "TEXT_MESSAGE_END", "messageId": streamID}})

			got := ansi.Strip(m.renderBlocks())
			if count := strings.Count(got, answer); count != 1 {
				t.Fatalf("assistant answer rendered %d times instead of once:\n%s", count, got)
			}
			if len(m.history) != 1 {
				t.Fatalf("duplicate assistant answer entered history: %#v", m.history)
			}
			if strings.Contains(got, "[TEXT_MESSAGE]") || strings.Contains(got, "#lc_run-") {
				t.Fatalf("protocol header or message id leaked into view:\n%s", got)
			}
		})
	}
}

func TestViewportCanScrollWhileRunIsActive(t *testing.T) {
	m := newTestModel()
	for i := range 80 {
		m.appendSystemBlock("LINE", fmt.Sprintf("%d", i), fmt.Sprintf("content %d", i))
	}
	m.running = true
	m.refreshViewport()
	bottom := m.viewport.YOffset
	if bottom == 0 {
		t.Fatal("test setup did not produce scrollable content")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(model)
	if m.viewport.YOffset >= bottom || m.viewport.AtBottom() {
		t.Fatalf("page up did not move away from bottom: before=%d after=%d", bottom, m.viewport.YOffset)
	}

	scrolledOffset := m.viewport.YOffset
	m.appendSystemBlock("LINE", "new", "new streamed content")
	m.refreshViewport()
	if m.viewport.YOffset != scrolledOffset {
		t.Fatalf("streamed output forced viewport back to bottom: before=%d after=%d", scrolledOffset, m.viewport.YOffset)
	}

	for range 3 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m = updated.(model)
		if m.viewport.AtBottom() {
			break
		}
	}
	if !m.viewport.AtBottom() {
		t.Fatalf("page down did not return to bottom: offset=%d", m.viewport.YOffset)
	}

	bottom = m.viewport.YOffset
	m.appendSystemBlock("LINE", "latest", "latest streamed content")
	m.refreshViewport()
	if !m.viewport.AtBottom() || m.viewport.YOffset <= bottom {
		t.Fatalf("viewport did not resume following output at bottom: before=%d after=%d", bottom, m.viewport.YOffset)
	}

	bottom = m.viewport.YOffset
	updated, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m = updated.(model)
	if m.viewport.YOffset >= bottom || m.viewport.AtBottom() {
		t.Fatalf("mouse wheel did not scroll away from bottom: before=%d after=%d", bottom, m.viewport.YOffset)
	}
}

func TestMouseTextSelectionAutoScrollsBeyondViewport(t *testing.T) {
	m := newTestModel()
	for i := range 80 {
		m.appendSystemBlock("LINE", fmt.Sprintf("%d", i), fmt.Sprintf("content %d", i))
	}
	m.followOutput = false
	m.refreshViewport()
	m.viewport.GotoTop()

	updated, _ := m.Update(tea.MouseMsg{
		X:      2,
		Y:      2,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	m = updated.(model)
	if !m.selection.dragging {
		t.Fatal("left mouse press did not begin application text selection")
	}

	viewportBottom := 1 + m.viewport.Height - 1
	updated, cmd := m.Update(tea.MouseMsg{
		X:      12,
		Y:      viewportBottom,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionMotion,
	})
	m = updated.(model)
	if m.viewport.YOffset == 0 {
		t.Fatal("dragging the application selection to the viewport bottom did not scroll down")
	}
	if !m.selection.active || cmd == nil {
		t.Fatal("dragging did not create a selection with continuous edge scrolling")
	}

	firstOffset := m.viewport.YOffset
	updated, _ = m.Update(selectionScrollTickMsg{token: m.selection.scrollToken})
	m = updated.(model)
	if m.viewport.YOffset <= firstOffset {
		t.Fatalf("edge-scroll tick did not continue downward: before=%d after=%d", firstOffset, m.viewport.YOffset)
	}

	updated, _ = m.Update(tea.MouseMsg{
		X:      12,
		Y:      viewportBottom,
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionRelease,
	})
	m = updated.(model)
	if m.selection.dragging || !m.selection.active {
		t.Fatal("mouse release did not finish and retain the text selection")
	}
	selected := m.selectedText()
	if !strings.Contains(selected, "content 0") || !strings.Contains(selected, "content") {
		t.Fatalf("selected text did not span the scrolled content:\n%s", selected)
	}
	if rendered := m.renderBlocks(); !strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("application text selection was not visibly highlighted:\n%s", rendered)
	}

	originalWriteClipboard := writeClipboard
	defer func() { writeClipboard = originalWriteClipboard }()
	copied := ""
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	updated, _ = m.Update(tea.MouseMsg{
		Y:      viewportBottom,
		Button: tea.MouseButtonRight,
		Action: tea.MouseActionPress,
	})
	m = updated.(model)
	if copied != selected {
		t.Fatalf("right click copied the wrong text:\nwant: %q\n got: %q", selected, copied)
	}
}

func TestCtrlVMultilinePasteShowsPlaceholderAndSendsOnce(t *testing.T) {
	originalReadClipboard := readClipboard
	defer func() { readClipboard = originalReadClipboard }()
	readClipboard = func() (string, error) {
		return "first line\r\nsecond line\r\nthird line", nil
	}

	m := newTestModel()
	initialViewportHeight := m.viewport.Height
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("ctrl+v did not request clipboard content")
	}

	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(model)
	if len(m.pastes) != 1 || m.pastes[0].lines != 3 {
		t.Fatalf("multiline clipboard content was not stored as one paste block: %#v", m.pastes)
	}
	if m.input.Value() != pasteBlockLabel(3) || len(m.history) != 0 {
		t.Fatalf("multiline paste entered or submitted the text input: input=%q history=%#v", m.input.Value(), m.history)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "[paste 3 lines]") {
		t.Fatalf("paste placeholder missing from input box:\n%s", view)
	}
	if strings.Contains(view, "first line") || strings.Contains(view, "second line") {
		t.Fatalf("hidden multiline paste leaked into the interface:\n%s", view)
	}
	if m.viewport.Height != initialViewportHeight {
		t.Fatalf("inline paste placeholder changed viewport height: before=%d after=%d", initialViewportHeight, m.viewport.Height)
	}
	placeholderLine := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "[paste 3 lines]") {
			placeholderLine = line
			break
		}
	}
	if placeholderLine == "" || !strings.Contains(placeholderLine, "> [paste 3 lines]") {
		t.Fatalf("paste placeholder was not rendered inline with the input prompt:\n%s", view)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if len(m.history) != 1 {
		t.Fatalf("enter submitted multiline paste %d times instead of once: %#v", len(m.history), m.history)
	}
	want := "first line\nsecond line\nthird line"
	if got := messageText(m.history[0].Content); got != want {
		t.Fatalf("submitted paste content changed:\nwant: %q\n got: %q", want, got)
	}
	if len(m.pastes) != 0 || m.pasteSummary() != "" || m.viewport.Height != initialViewportHeight {
		t.Fatalf("paste placeholder was not cleared after submit: pastes=%#v height=%d", m.pastes, m.viewport.Height)
	}
}

func TestWindowsConsoleInjectedMultilinePasteUsesPlaceholder(t *testing.T) {
	m := newTestModel()
	m.windowsPasteCaptureEnabled = true
	clipboardContent := "first line\r\nsecond line\r\n第三行"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}})
	m = updated.(model)
	if cmd == nil || m.terminalPaste == nil {
		t.Fatal("Windows paste prelude did not start clipboard verification")
	}
	probeID := m.terminalPaste.id

	// Windows' default console reader can deliver pasted characters before the
	// asynchronous clipboard read completes. They must remain hidden and Enter
	// must not submit the first line.
	for _, keyMsg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("first line")},
		{Type: tea.KeyEnter},
	} {
		updated, _ = m.Update(keyMsg)
		m = updated.(model)
	}
	if m.input.Value() != "" || len(m.history) != 0 {
		t.Fatalf("pending injected paste leaked or submitted before clipboard verification: input=%q history=%#v", m.input.Value(), m.history)
	}

	updated, _ = m.Update(clipboardPasteMsg{probeID: probeID, content: clipboardContent})
	m = updated.(model)
	for _, keyMsg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("second line")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("第三行")},
	} {
		updated, _ = m.Update(keyMsg)
		m = updated.(model)
	}

	want := normalizePastedText(clipboardContent)
	if m.terminalPaste != nil || len(m.pastes) != 1 || m.pastes[0].content != want {
		t.Fatalf("injected console stream was not converted to one paste block: capture=%#v pastes=%#v", m.terminalPaste, m.pastes)
	}
	if m.input.Value() != pasteBlockLabel(3) || len(m.history) != 0 {
		t.Fatalf("verified injected paste leaked or submitted: input=%q history=%#v", m.input.Value(), m.history)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "> [paste 3 lines]") || strings.Contains(view, "first line") {
		t.Fatalf("verified paste did not render as an inline hidden placeholder:\n%s", view)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if len(m.history) != 1 || messageText(m.history[0].Content) != want {
		t.Fatalf("verified paste was not sent exactly once: %#v", m.history)
	}
}

func TestWindowsPasteProbeMismatchPreservesChineseInput(t *testing.T) {
	m := newTestModel()
	m.windowsPasteCaptureEnabled = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}})
	m = updated.(model)
	probeID := m.terminalPaste.id
	updated, _ = m.Update(clipboardPasteMsg{probeID: probeID, content: "clipboard\ntext"})
	m = updated.(model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("中文输入")})
	m = updated.(model)
	if m.terminalPaste != nil || len(m.pastes) != 0 || m.input.Value() != "中文输入" {
		t.Fatalf("paste probe mismatch damaged Unicode input: capture=%#v pastes=%#v input=%q", m.terminalPaste, m.pastes, m.input.Value())
	}
}

func TestWindowsPasteProbeTimeoutReplaysBufferedInput(t *testing.T) {
	m := newTestModel()
	m.windowsPasteCaptureEnabled = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}})
	m = updated.(model)
	probeID := m.terminalPaste.id
	updated, _ = m.Update(clipboardPasteMsg{probeID: probeID, content: "partial text\nsecond line"})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("partial")})
	m = updated.(model)
	token := m.terminalPaste.timeoutToken

	updated, _ = m.Update(terminalPasteTimeoutMsg{probeID: probeID, token: token})
	m = updated.(model)
	if m.terminalPaste != nil || len(m.pastes) != 0 || m.input.Value() != "partial" {
		t.Fatalf("timed-out paste probe did not replay buffered input: capture=%#v pastes=%#v input=%q", m.terminalPaste, m.pastes, m.input.Value())
	}
}

func TestBracketedMultilinePasteUsesPlaceholder(t *testing.T) {
	m := newTestModel()
	content := "alpha\nbeta"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(content), Paste: true})
	m = updated.(model)

	if len(m.pastes) != 1 || m.pastes[0].content != content || m.input.Value() != pasteBlockLabel(2) || len(m.history) != 0 {
		t.Fatalf("bracketed multiline paste was not captured atomically: pastes=%#v input=%q history=%#v", m.pastes, m.input.Value(), m.history)
	}
}

func TestMultilinePasteTokenUsesCursorPositionAndExpandsInPlace(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("before-after")
	position := len([]rune("before-"))
	m.input.SetCursor(position)

	content := "第一行\n第二行"
	m.handlePastedText(content)
	label := pasteBlockLabel(2)
	wantVisible := "before-" + label + "after"
	if m.input.Value() != wantVisible {
		t.Fatalf("multiline paste token was not inserted at the cursor:\nwant: %q\n got: %q", wantVisible, m.input.Value())
	}
	if m.input.Position() != position+len([]rune(label)) {
		t.Fatalf("cursor was not placed after the paste token: want=%d got=%d", position+len([]rune(label)), m.input.Position())
	}
	if got, want := m.composedInput(), "before-"+content+"after"; got != want {
		t.Fatalf("paste token expanded at the wrong message position:\nwant: %q\n got: %q", want, got)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, wantVisible) {
		t.Fatalf("input view did not render the token at the cursor position:\n%s", view)
	}
}

func TestPasteTokenTracksEditsAndMovesAtomically(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("前后")
	m.input.SetCursor(1)
	m.handlePastedText("甲\n乙")
	label := pasteBlockLabel(2)
	tokenStart := 1
	tokenEnd := tokenStart + len([]rune(label))

	// Typing at the token's end must keep the token between the surrounding
	// Chinese text while updating its stored range.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("中文")})
	m = updated.(model)
	if got, want := m.input.Value(), "前"+label+"中文后"; got != want {
		t.Fatalf("typing after paste token changed its position:\nwant: %q\n got: %q", want, got)
	}
	if len(m.pastes) != 1 || m.pastes[0].start != tokenStart || m.pastes[0].end != tokenEnd {
		t.Fatalf("paste token range changed incorrectly after typing: %#v", m.pastes)
	}
	m.input.SetCursor(tokenStart)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("新")})
	m = updated.(model)
	tokenStart++
	tokenEnd++
	if got, want := m.input.Value(), "前新"+label+"中文后"; got != want {
		t.Fatalf("typing before paste token changed its position:\nwant: %q\n got: %q", want, got)
	}
	if m.pastes[0].start != tokenStart || m.pastes[0].end != tokenEnd {
		t.Fatalf("paste token range did not shift after typing before it: %#v", m.pastes)
	}

	m.input.SetCursor(tokenEnd)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)
	if m.input.Position() != tokenStart {
		t.Fatalf("left arrow did not cross paste token atomically: want=%d got=%d", tokenStart, m.input.Position())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)
	if m.input.Position() != tokenEnd {
		t.Fatalf("right arrow did not cross paste token atomically: want=%d got=%d", tokenEnd, m.input.Position())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(model)
	if len(m.pastes) != 0 || m.input.Value() != "前新中文后" || m.input.Position() != tokenStart {
		t.Fatalf("backspace did not remove paste token atomically: pastes=%#v input=%q cursor=%d", m.pastes, m.input.Value(), m.input.Position())
	}

	m.handlePastedText("再甲\n再乙")
	deleteStart := m.pastes[0].start
	m.input.SetCursor(deleteStart)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	m = updated.(model)
	if len(m.pastes) != 0 || m.input.Value() != "前新中文后" || m.input.Position() != deleteStart {
		t.Fatalf("delete did not remove paste token atomically: pastes=%#v input=%q cursor=%d", m.pastes, m.input.Value(), m.input.Position())
	}
}

func TestMultiplePasteTokensKeepDraftOrder(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("AC")
	m.input.SetCursor(1)
	m.handlePastedText("one\ntwo")
	m.input.CursorEnd()
	m.handlePastedText("three\nfour\nfive")

	if got, want := m.composedInput(), "Aone\ntwoCthree\nfour\nfive"; got != want {
		t.Fatalf("multiple paste tokens expanded out of draft order:\nwant: %q\n got: %q", want, got)
	}
	if len(m.pastes) != 2 || m.pastes[0].start >= m.pastes[1].start {
		t.Fatalf("paste token ranges are not ordered: %#v", m.pastes)
	}
}

func TestCtrlVSingleLinePasteInsertsAtCursor(t *testing.T) {
	originalReadClipboard := readClipboard
	defer func() { readClipboard = originalReadClipboard }()
	readClipboard = func() (string, error) { return "inserted", nil }

	m := newTestModel()
	m.input.SetValue("before-after")
	m.input.SetCursor(len([]rune("before-")))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = updated.(model)
	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(model)

	if got, want := m.input.Value(), "before-insertedafter"; got != want {
		t.Fatalf("single-line paste inserted at the wrong position: want=%q got=%q", want, got)
	}
	if len(m.pastes) != 0 || len(m.history) != 0 {
		t.Fatalf("single-line paste unexpectedly created a paste block or submitted: pastes=%#v history=%#v", m.pastes, m.history)
	}
}

func TestPasteBlockPreventsCommandExecution(t *testing.T) {
	m := newTestModel()
	threadID := m.threadID
	content := "/new\nthis is pasted text"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(content), Paste: true})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if m.threadID != threadID || len(m.history) != 1 || messageText(m.history[0].Content) != content {
		t.Fatalf("paste content was interpreted as a command: thread=%q history=%#v", m.threadID, m.history)
	}
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()
	select {
	case msg := <-done:
		return msg
	case <-time.After(time.Second):
		t.Fatal("command did not return")
		return nil
	}
}

func TestCancelCommandResetsActiveRunID(t *testing.T) {
	m := newTestModel()
	m.activeRunID = "test-run-1"
	m.running = true
	m.stream = &agui.Stream{RunID: "test-run-1"}

	m.input.SetValue("/cancel")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if m.activeRunID != "" {
		t.Fatalf("expected activeRunID to be cleared after /cancel, got %q", m.activeRunID)
	}
	if m.running {
		t.Fatal("expected running=false after /cancel")
	}
	if m.status != "Idle (cancelled)" {
		t.Fatalf("expected status 'Idle (cancelled)', got %q", m.status)
	}
}

func TestCancelServerRunNoopWhenNoActiveRun(t *testing.T) {
	m := newTestModel()
	m.activeRunID = ""
	m.cancelServerRun()
}
