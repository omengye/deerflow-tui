package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"deerflow-tui/internal/agui"
	"deerflow-tui/internal/config"
)

const emptyThreadText = "(No messages yet. Type to start.)"

type runStartResultMsg struct {
	runSeq uint64
	stream *agui.Stream
	err    error
}

type aguiEventMsg struct {
	runSeq uint64
	event  agui.EventEnvelope
}

type aguiErrorMsg struct {
	runSeq uint64
	err    error
}

type streamDoneMsg struct {
	runSeq uint64
}

type displayBlock struct {
	header  string
	content string
}

type toolCallBuffer struct {
	id      string
	name    string
	args    strings.Builder
	result  string
	isError bool
	ended   bool
}

type replayState struct {
	historicalIDs          map[string]struct{}
	historicalTexts        []string
	ignoredTextMessageIDs  map[string]struct{}
	completedTextMessageID map[string]struct{}
}

type model struct {
	cfg    config.Config
	client *agui.Client

	width  int
	height int

	viewport viewport.Model
	input    textinput.Model

	status   string
	threadID string
	starting bool
	running  bool
	runSeq   uint64

	blocks           []displayBlock
	blockIndexes     map[string]int
	history          []agui.ChatMessage
	interrupts       []agui.Interrupt
	resumeParent     string
	selectedBlockIdx int
	lineToBlock      []int

	stream          *agui.Stream
	asyncCh         chan tea.Msg
	replay          replayState
	textBuffers     map[string]*strings.Builder
	textAgentNames  map[string]string
	activeTextID    string
	textPartCounter int
	reasoningBuffer map[string]*strings.Builder
	activeReasonID  string
	reasonCounter   int
	thinkingBuffer  strings.Builder
	toolBuffers     map[string]*toolCallBuffer
	toolCounter     int
}

func NewModel(cfg config.Config) tea.Model {
	input := textinput.New()
	input.Placeholder = "Type a message (/new, /exit, /quit, /cancel)..."
	input.Focus()
	input.CharLimit = 16000
	input.Width = 60

	vp := viewport.New(80, 20)
	vp.SetContent(emptyThreadText)

	return model{
		cfg:              cfg,
		client:           agui.NewClient(cfg.Endpoint, cfg.Headers, cfg.InitialState),
		viewport:         vp,
		input:            input,
		status:           "Idle",
		threadID:         mustThreadID(),
		asyncCh:          make(chan tea.Msg, 512),
		blockIndexes:     map[string]int{},
		replay:           newReplayState(nil),
		textBuffers:      map[string]*strings.Builder{},
		textAgentNames:   map[string]string{},
		reasoningBuffer:  map[string]*strings.Builder{},
		toolBuffers:      map[string]*toolCallBuffer{},
		selectedBlockIdx: -1,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.refreshViewport()

	case tea.KeyMsg:
		switch {
		case keyMatches(msg, "ctrl+c"):
			m.copySelectedBlock()
		case keyMatches(msg, "enter"):
			cmd := m.handleSubmit()
			m.layout()
			m.refreshViewport()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			m.handleLeftClick(msg.Y)
		}

	case runStartResultMsg:
		if msg.runSeq != m.runSeq {
			if msg.stream != nil {
				msg.stream.Close()
			}
			break
		}

		if msg.err != nil {
			m.starting = false
			m.running = false
			m.status = fmt.Sprintf("Run error: %v", msg.err)
			m.appendSystemBlock("RUN_ERROR", "", msg.err.Error())
			break
		}

		m.stream = msg.stream
		m.starting = false
		m.running = true
		m.status = "Running"
		bridgeStream(msg.runSeq, msg.stream, m.asyncCh)
		cmds = append(cmds, waitForAsync(m.asyncCh))

	case aguiEventMsg:
		if msg.runSeq != m.runSeq {
			break
		}
		m.handleEvent(msg.event)
		cmds = append(cmds, waitForAsync(m.asyncCh))

	case aguiErrorMsg:
		if msg.runSeq != m.runSeq {
			break
		}
		m.starting = false
		m.running = false
		m.status = fmt.Sprintf("Stream error: %v", msg.err)
		m.appendSystemBlock("RUN_ERROR", "", msg.err.Error())
		cmds = append(cmds, waitForAsync(m.asyncCh))

	case streamDoneMsg:
		if msg.runSeq != m.runSeq {
			break
		}
		m.starting = false
		m.running = false
		m.stream = nil
		if strings.HasPrefix(m.status, "Running") || m.status == "Running" {
			m.status = "Idle"
		}

	case nil:
		// no-op
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	m.refreshViewport()

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("deerflow-tui") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" - AG-UI Bubble Tea rewrite")

	inputLine := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1).Render(
		m.input.View(),
	)

	status := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		fmt.Sprintf("%s | Messages: %d | Thread: %s | %s", m.status, len(m.history), shortID(m.threadID), m.cfg.Endpoint),
	)

	frame := lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View(), inputLine, status)
	// Clamp the frame to the terminal bounds. Any line wider than the terminal
	// wraps and desyncs Bubble Tea's diff renderer, leaving stale content from
	// previous frames on screen (e.g. after /new). MaxWidth truncates such lines
	// instead of letting the terminal wrap them.
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(frame)
}

func (m *model) handleSubmit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	m.input.SetValue("")

	switch strings.ToLower(text) {
	case "/exit", "/quit":
		m.stopStream()
		return tea.Quit
	case "/cancel":
		m.stopStream()
		m.clearInterrupts()
		m.status = "Idle (cancelled)"
		return nil
	case "/new":
		m.stopStream()
		m.threadID = mustThreadID()
		m.history = nil
		m.clearInterrupts()
		m.blocks = nil
		m.blockIndexes = map[string]int{}
		m.replay = newReplayState(nil)
		m.selectedBlockIdx = -1
		m.lineToBlock = nil
		m.textPartCounter = 0
		m.reasonCounter = 0
		m.toolCounter = 0
		m.status = "Idle (new thread)"
		m.viewport.SetYOffset(0)
		return nil
	}

	if len(m.interrupts) > 0 {
		return m.handleInterruptResponse(text)
	}

	if m.running || m.starting {
		m.stopStream()
		m.status = "Restarting run..."
	}

	m.appendChatBlock("You", "", text)
	m.history = append(m.history, agui.ChatMessage{Role: agui.RoleUser, Content: text})

	history := append([]agui.ChatMessage(nil), m.history...)
	threadID := m.threadID
	runSeq := m.beginRun(history)
	m.status = "Starting run..."

	return func() tea.Msg {
		stream, err := m.client.StartRun(context.Background(), threadID, history)
		return runStartResultMsg{runSeq: runSeq, stream: stream, err: err}
	}
}

func (m *model) handleInterruptResponse(text string) tea.Cmd {
	responses, err := m.buildResumeEntries(text)
	if err != nil {
		m.status = "Interrupt response error"
		m.appendSystemBlock("INTERRUPT_ERROR", "", err.Error())
		return nil
	}

	history := append([]agui.ChatMessage(nil), m.history...)
	threadID := m.threadID
	runSeq := m.beginRun(history)
	m.status = "Resuming run..."
	m.clearInterrupts()

	return func() tea.Msg {
		stream, err := m.client.ResumeRun(context.Background(), threadID, history, responses)
		return runStartResultMsg{runSeq: runSeq, stream: stream, err: err}
	}
}

func (m *model) handleEvent(event agui.EventEnvelope) {
	typeName := strings.TrimSpace(event.Type)
	if typeName == "" {
		typeName = "RAW"
	}

	switch typeName {
	case "RUN_STARTED":
		m.running = true
		m.status = "Running"
		m.appendSystemBlock("RUN_STARTED", valueString(event.Raw, "runId"), "")

	case "RUN_FINISHED":
		m.finalizeRun()
		if interrupts := agui.InterruptsFromRunFinished(event); len(interrupts) > 0 {
			m.running = false
			m.status = "Requires interrupt response"
			m.interrupts = interrupts
			m.appendInterruptBlock(interrupts)
			return
		}
		m.running = false
		m.status = "Idle"
		m.appendSystemBlock("RUN_FINISHED", valueString(event.Raw, "runId"), compactJSON(event.Raw))

	case "RUN_CANCELLED":
		m.running = false
		m.status = "Cancelled"
		m.finalizeRun()
		m.appendSystemBlock("RUN_CANCELLED", valueString(event.Raw, "runId"), "")

	case "RUN_ERROR":
		m.running = false
		m.status = "Run error"
		m.finalizeRun()
		m.appendSystemBlock("RUN_ERROR", "", valueString(event.Raw, "message"))

	case "TEXT_MESSAGE_START":
		id := m.resolveTextID(event.MessageID, true)
		if m.shouldIgnoreTextStart(id) {
			return
		}
		m.recordTextAgentName(id, event)
		m.ensureTextBuffer(id)
		m.upsertBlock(textBlockKey(id), m.textMessageHeader(id), "")

	case "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CHUNK":
		id := m.resolveTextID(event.MessageID, false)
		if m.shouldIgnoreTextContent(id) {
			return
		}
		m.recordTextAgentName(id, event)
		if event.Delta == "" {
			if buf, ok := m.textBuffers[id]; ok {
				m.upsertBlock(textBlockKey(id), m.textMessageHeader(id), buf.String())
			}
			return
		}
		buf := m.ensureTextBuffer(id)
		buf.WriteString(event.Delta)
		m.upsertBlock(textBlockKey(id), m.textMessageHeader(id), buf.String())

	case "TEXT_MESSAGE_END":
		id := m.resolveTextID(event.MessageID, false)
		if m.finishIgnoredText(id) {
			return
		}
		m.recordTextAgentName(id, event)
		buf := m.ensureTextBuffer(id)
		text := strings.TrimSpace(buf.String())
		filtered := m.replay.filterReplayText(text)
		if filtered == "" {
			m.removeBlock(textBlockKey(id))
			delete(m.textBuffers, id)
			delete(m.textAgentNames, id)
			m.clearActiveText(id)
			return
		}
		m.upsertBlock(textBlockKey(id), m.textMessageHeader(id), filtered)
		m.history = append(m.history, agui.ChatMessage{Role: agui.RoleAssistant, Content: filtered, ID: id, Name: m.textAgentNames[id]})
		m.replay.completedTextMessageID[id] = struct{}{}
		delete(m.textBuffers, id)
		delete(m.textAgentNames, id)
		m.clearActiveText(id)

	case "THINKING_START", "THINKING_TEXT_MESSAGE_START":
		m.thinkingBuffer.Reset()
		title := valueString(event.Raw, "title")
		m.upsertBlock("thinking", "[THINKING]", title)

	case "THINKING_TEXT_MESSAGE_CONTENT":
		m.thinkingBuffer.WriteString(event.Delta)
		m.upsertBlock("thinking", "[THINKING]", m.thinkingBuffer.String())

	case "THINKING_TEXT_MESSAGE_END", "THINKING_END":
		thinking := strings.TrimSpace(m.thinkingBuffer.String())
		if thinking != "" {
			m.upsertBlock("thinking", "[THINKING]", thinking)
		} else {
			m.removeBlock("thinking")
		}
		m.thinkingBuffer.Reset()

	case "REASONING_START", "REASONING_MESSAGE_START":
		id := m.resolveReasoningID(event.MessageID, true)
		m.ensureReasoningBuffer(id)
		m.upsertBlock(reasoningBlockKey(id), eventHeader("REASONING", id), "")

	case "REASONING_MESSAGE_CONTENT":
		id := m.resolveReasoningID(event.MessageID, false)
		buf := m.ensureReasoningBuffer(id)
		buf.WriteString(event.Delta)
		m.upsertBlock(reasoningBlockKey(id), eventHeader("REASONING", id), buf.String())

	case "REASONING_MESSAGE_END", "REASONING_END":
		id := m.resolveReasoningID(event.MessageID, false)
		buf := m.ensureReasoningBuffer(id)
		reasoning := strings.TrimSpace(m.replay.filterReplayText(buf.String()))
		if reasoning != "" {
			m.upsertBlock(reasoningBlockKey(id), eventHeader("REASONING", id), reasoning)
		} else {
			m.removeBlock(reasoningBlockKey(id))
		}
		delete(m.reasoningBuffer, id)
		m.clearActiveReasoning(id)

	case "TOOL_CALL_START":
		toolID := m.resolveToolID(valueString(event.Raw, "toolCallId"))
		name := valueString(event.Raw, "toolCallName")
		m.toolBuffers[toolID] = &toolCallBuffer{id: toolID, name: name}
		m.upsertBlock(toolBlockKey(toolID), eventHeader("TOOL_CALL", toolID), strings.TrimSpace(name))
		m.recordToolCallStart(toolID, name)

	case "TOOL_CALL_ARGS", "TOOL_CALL_CHUNK":
		toolID := m.resolveToolID(valueString(event.Raw, "toolCallId"))
		buf, ok := m.toolBuffers[toolID]
		if !ok {
			buf = &toolCallBuffer{id: toolID, name: valueString(event.Raw, "toolCallName")}
			m.toolBuffers[toolID] = buf
		}
		buf.args.WriteString(event.Delta)
		m.upsertBlock(toolBlockKey(toolID), eventHeader("TOOL_CALL", toolID), formatToolCall(buf))
		m.updateToolCallArgs(toolID, buf.name, buf.args.String())

	case "TOOL_CALL_END":
		toolID := m.resolveToolID(valueString(event.Raw, "toolCallId"))
		if buf, ok := m.toolBuffers[toolID]; ok {
			buf.ended = true
			m.upsertBlock(toolBlockKey(toolID), eventHeader("TOOL_CALL", toolID), formatToolCall(buf))
		} else {
			m.upsertBlock(toolBlockKey(toolID), eventHeader("TOOL_CALL", toolID), "")
		}

	case "TOOL_CALL_RESULT":
		toolID := m.resolveToolID(valueString(event.Raw, "toolCallId"))
		content := valueString(event.Raw, "content")
		if content == "" {
			content = compactJSON(event.Raw)
		}
		buf, ok := m.toolBuffers[toolID]
		if !ok {
			buf = &toolCallBuffer{id: toolID, name: "tool"}
			m.toolBuffers[toolID] = buf
		}
		buf.result = content
		buf.isError = valueBool(event.Raw, "isError") || valueString(event.Raw, "role") != "tool" && valueString(event.Raw, "role") != ""
		m.upsertBlock(toolBlockKey(toolID), eventHeader("TOOL_CALL", toolID), formatToolCall(buf))
		m.recordToolResult(toolID, content, buf.isError)

	case "STATE_SNAPSHOT", "STATE_DELTA":
		// internal state tracking only, not displayed

	case "CUSTOM", "SYSTEM":
		// custom and system events are not displayed

	case "MESSAGES_SNAPSHOT":
		m.importMessagesSnapshot(event.Raw["messages"])

	case "RAW":
		m.appendSystemBlock(typeName, valueString(event.Raw, "source"), compactJSON(event.Raw))

	default:
		m.appendSystemBlock(typeName, "", compactJSON(event.Raw))
	}
}

func (m *model) appendInterruptBlock(interrupts []agui.Interrupt) {
	var lines []string
	for _, interrupt := range interrupts {
		parts := []string{fmt.Sprintf("%s (%s)", interrupt.ID, interrupt.Reason)}
		if interrupt.Message != "" {
			parts = append(parts, interrupt.Message)
		}
		if interrupt.ToolCallID != "" {
			parts = append(parts, "toolCall="+interrupt.ToolCallID)
		}
		lines = append(lines, strings.Join(parts, " - "))
	}
	lines = append(lines, "Reply with text to resolve all interrupts, or JSON array of {interruptId,status,payload}.")
	m.upsertBlock("interrupts", "[INTERRUPT]", strings.Join(lines, "\n"))
}

func (m *model) clearInterrupts() {
	m.interrupts = nil
	m.removeBlock("interrupts")
}

func (m *model) buildResumeEntries(text string) ([]agui.ResumeEntry, error) {
	var parsed []agui.ResumeEntry
	if strings.HasPrefix(strings.TrimSpace(text), "[") {
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			return nil, fmt.Errorf("invalid interrupt JSON: %w", err)
		}
	} else {
		parsed = make([]agui.ResumeEntry, 0, len(m.interrupts))
		for _, interrupt := range m.interrupts {
			parsed = append(parsed, agui.ResumeEntry{InterruptID: interrupt.ID, Status: "resolved", Payload: text})
		}
	}
	return validateResumeEntries(m.interrupts, parsed)
}

func validateResumeEntries(interrupts []agui.Interrupt, responses []agui.ResumeEntry) ([]agui.ResumeEntry, error) {
	open := map[string]agui.Interrupt{}
	for _, interrupt := range interrupts {
		open[interrupt.ID] = interrupt
	}
	seen := map[string]struct{}{}
	for _, response := range responses {
		if response.InterruptID == "" {
			return nil, fmt.Errorf("every interrupt response must include interruptId")
		}
		if response.Status != "resolved" && response.Status != "cancelled" {
			return nil, fmt.Errorf("invalid status %q for interrupt %s", response.Status, response.InterruptID)
		}
		if _, ok := seen[response.InterruptID]; ok {
			return nil, fmt.Errorf("duplicate response for interrupt %s", response.InterruptID)
		}
		seen[response.InterruptID] = struct{}{}
		interrupt, ok := open[response.InterruptID]
		if !ok {
			return nil, fmt.Errorf("unknown interrupt id %s", response.InterruptID)
		}
		if interrupt.ExpiresAt != "" {
			expiresAt, err := time.Parse(time.RFC3339, interrupt.ExpiresAt)
			if err != nil {
				return nil, fmt.Errorf("interrupt %s has malformed expiresAt %q", interrupt.ID, interrupt.ExpiresAt)
			}
			if !expiresAt.After(time.Now()) {
				return nil, fmt.Errorf("interrupt %s expired at %s", interrupt.ID, interrupt.ExpiresAt)
			}
		}
	}
	for _, interrupt := range interrupts {
		if _, ok := seen[interrupt.ID]; !ok {
			return nil, fmt.Errorf("missing response for interrupt %s", interrupt.ID)
		}
	}
	return responses, nil
}

func (m *model) importMessagesSnapshot(raw any) {
	messages := agui.MessagesFromSnapshot(raw)
	if len(messages) == 0 {
		return
	}
	m.history = messages
	m.blocks = nil
	m.blockIndexes = map[string]int{}
	for _, message := range messages {
		m.renderHistoryMessage(message)
	}
}

func (m *model) renderHistoryMessage(message agui.ChatMessage) {
	switch agui.NormalizeRole(message.Role) {
	case agui.RoleUser:
		m.appendChatBlock("You", message.ID, messageText(message.Content))
	case agui.RoleAssistant:
		text := messageText(message.Content)
		if text != "" {
			m.appendChatBlock(speakerWithName("Assistant", message.Name), message.ID, text)
		}
		for _, call := range message.ToolCalls {
			buf := &toolCallBuffer{id: call.ID, name: call.Function.Name}
			buf.args.WriteString(call.Function.Arguments)
			m.upsertBlock(toolBlockKey(call.ID), eventHeader("TOOL_CALL", call.ID), formatToolCall(buf))
		}
	case agui.RoleTool:
		m.appendSystemBlock("TOOL", message.ToolCallID, messageText(message.Content))
	case agui.RoleSystem:
		// system messages are not displayed
	}
}

func (m *model) recordToolResult(toolID, content string, isError bool) {
	for index := len(m.history) - 1; index >= 0; index-- {
		message := &m.history[index]
		if message.Role != agui.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID == toolID {
				toolMessage := agui.ChatMessage{Role: agui.RoleTool, Content: content, ToolCallID: toolID}
				if isError {
					toolMessage.Error = content
				}
				m.history = append(m.history, toolMessage)
				return
			}
		}
	}
	toolMessage := agui.ChatMessage{Role: agui.RoleTool, Content: content, ToolCallID: toolID}
	if isError {
		toolMessage.Error = content
	}
	m.history = append(m.history, toolMessage)
}

func (m *model) recordToolCallStart(toolID, name string) {
	if toolID == "" {
		return
	}
	args := "{}"
	if buf, ok := m.toolBuffers[toolID]; ok {
		args = strings.TrimSpace(buf.args.String())
		if args == "" {
			args = "{}"
		}
	}
	call := agui.NewToolCall(toolID, name, args)
	for index := len(m.history) - 1; index >= 0; index-- {
		message := &m.history[index]
		if message.Role != agui.RoleAssistant {
			continue
		}
		for _, existing := range message.ToolCalls {
			if existing.ID == toolID {
				return
			}
		}
		message.ToolCalls = append(message.ToolCalls, call)
		return
	}
	m.history = append(m.history, agui.ChatMessage{Role: agui.RoleAssistant, ID: toolID + ":assistant", Content: "", ToolCalls: []agui.ToolCall{call}})
}

func (m *model) updateToolCallArgs(toolID, name, args string) {
	if toolID == "" {
		return
	}
	for index := len(m.history) - 1; index >= 0; index-- {
		message := &m.history[index]
		if message.Role != agui.RoleAssistant {
			continue
		}
		for callIndex, call := range message.ToolCalls {
			if call.ID != toolID {
				continue
			}
			if name == "" {
				name = call.Function.Name
			}
			message.ToolCalls[callIndex] = agui.NewToolCall(toolID, name, args)
			return
		}
	}
	m.recordToolCallStart(toolID, name)
}

func (m *model) appendChatBlock(speaker, id, text string) {
	header := speaker + ":"
	if id != "" {
		header = eventHeader(speaker, id)
	}
	m.appendBlock(displayBlock{header: header, content: strings.TrimSpace(text)})
}

func (m *model) appendSystemBlock(eventType, id, detail string) {
	m.appendBlock(displayBlock{header: eventHeader(eventType, id), content: strings.TrimSpace(detail)})
}

func (m *model) recordTextAgentName(id string, event agui.EventEnvelope) {
	if id == "" {
		return
	}
	name := eventAgentName(event.Raw)
	if name != "" {
		m.textAgentNames[id] = name
	}
}

func (m *model) textMessageHeader(id string) string {
	return headerWithAgent(eventHeader("TEXT_MESSAGE", id), m.textAgentNames[id])
}

func (m *model) appendBlock(block displayBlock) {
	m.blocks = append(m.blocks, block)
}

func (m *model) upsertBlock(key, header, content string) {
	if idx, ok := m.blockIndexes[key]; ok && idx >= 0 && idx < len(m.blocks) {
		m.blocks[idx] = displayBlock{header: header, content: strings.TrimSpace(content)}
		return
	}
	m.blockIndexes[key] = len(m.blocks)
	m.blocks = append(m.blocks, displayBlock{header: header, content: strings.TrimSpace(content)})
}

func (m *model) removeBlock(key string) {
	idx, ok := m.blockIndexes[key]
	if !ok || idx < 0 || idx >= len(m.blocks) {
		return
	}
	m.blocks = append(m.blocks[:idx], m.blocks[idx+1:]...)
	delete(m.blockIndexes, key)
	for k, v := range m.blockIndexes {
		if v > idx {
			m.blockIndexes[k] = v - 1
		}
	}
}

func (m *model) renderBlocks() string {
	m.lineToBlock = nil
	if len(m.blocks) == 0 {
		// Fill the entire viewport height with space-padded lines to prevent
		// lipgloss's Height() from appending bare '\n' lines (which leave old
		// terminal content uncleared).  This ensures every row is overwritten.
		if m.viewport.Height > 1 && m.viewport.Width > 0 {
			var b strings.Builder
			b.WriteString(emptyThreadText)
			blankLine := "\n" + strings.Repeat(" ", m.viewport.Width)
			for range m.viewport.Height - 1 {
				b.WriteString(blankLine)
			}
			return b.String()
		}
		return emptyThreadText
	}

	var out []string
	for i, block := range m.blocks {
		prefix := "  "
		if i == m.selectedBlockIdx {
			prefix = "▸ "
		}
		m.lineToBlock = append(m.lineToBlock, i)
		out = append(out, prefix+block.header)
		content := strings.TrimSpace(block.content)
		if content != "" {
			wrapWidth := m.viewport.Width - 4
			if wrapWidth < 20 {
				wrapWidth = 80
			}
			wrapped := lipgloss.NewStyle().Width(wrapWidth).Render(content)
			for _, line := range strings.Split(wrapped, "\n") {
				m.lineToBlock = append(m.lineToBlock, i)
				out = append(out, "  "+line)
			}
		}
	}

	// Pad the output to the viewport height with blank space-padded lines to prevent
	// old terminal content from remaining visible.
	if len(out) < m.viewport.Height && m.viewport.Width > 0 {
		blankLine := strings.Repeat(" ", m.viewport.Width)
		for len(out) < m.viewport.Height {
			out = append(out, blankLine)
		}
	}

	return strings.Join(out, "\n")
}

func (m *model) resolveTextID(id string, start bool) string {
	id = strings.TrimSpace(id)
	if id != "" {
		if start {
			m.activeTextID = id
		}
		return id
	}
	if start || m.activeTextID == "" {
		m.textPartCounter++
		m.activeTextID = fmt.Sprintf("text-%d", m.textPartCounter)
	}
	return m.activeTextID
}

func (m *model) clearActiveText(id string) {
	if m.activeTextID == id {
		m.activeTextID = ""
	}
}

func (m *model) resolveReasoningID(id string, start bool) string {
	id = strings.TrimSpace(id)
	if id != "" {
		if start {
			m.activeReasonID = id
		}
		return id
	}
	if start || m.activeReasonID == "" {
		m.reasonCounter++
		m.activeReasonID = fmt.Sprintf("reasoning-%d", m.reasonCounter)
	}
	return m.activeReasonID
}

func (m *model) clearActiveReasoning(id string) {
	if m.activeReasonID == id {
		m.activeReasonID = ""
	}
}

func (m *model) resolveToolID(id string) string {
	id = strings.TrimSpace(id)
	if id != "" {
		return id
	}
	m.toolCounter++
	return fmt.Sprintf("tool-%d", m.toolCounter)
}

func (m *model) ensureTextBuffer(id string) *strings.Builder {
	if buf, ok := m.textBuffers[id]; ok {
		return buf
	}
	buf := &strings.Builder{}
	m.textBuffers[id] = buf
	return buf
}

func (m *model) ensureReasoningBuffer(id string) *strings.Builder {
	if buf, ok := m.reasoningBuffer[id]; ok {
		return buf
	}
	buf := &strings.Builder{}
	m.reasoningBuffer[id] = buf
	return buf
}

func (m *model) shouldIgnoreTextStart(id string) bool {
	if id == "" {
		return false
	}
	normalizedID := normalizeHistoricalTextMessageID(id)
	if _, ok := m.replay.historicalIDs[normalizedID]; ok {
		m.replay.ignoredTextMessageIDs[id] = struct{}{}
		return true
	}
	if _, ok := m.replay.completedTextMessageID[id]; ok {
		m.replay.ignoredTextMessageIDs[id] = struct{}{}
		return true
	}
	return false
}

func (m *model) shouldIgnoreTextContent(id string) bool {
	_, ok := m.replay.ignoredTextMessageIDs[id]
	return ok
}

func (m *model) finishIgnoredText(id string) bool {
	if _, ok := m.replay.ignoredTextMessageIDs[id]; !ok {
		return false
	}
	delete(m.replay.ignoredTextMessageIDs, id)
	delete(m.textBuffers, id)
	delete(m.textAgentNames, id)
	m.removeBlock(textBlockKey(id))
	m.clearActiveText(id)
	return true
}

func (m *model) finalizeRun() {
	// Flush text
	for id, buf := range m.textBuffers {
		text := strings.TrimSpace(m.replay.filterReplayText(buf.String()))
		if text == "" {
			m.removeBlock(textBlockKey(id))
			continue
		}
		m.upsertBlock(textBlockKey(id), m.textMessageHeader(id), text)
		m.history = append(m.history, agui.ChatMessage{Role: agui.RoleAssistant, Content: text, ID: id, Name: m.textAgentNames[id]})
	}
	m.textBuffers = map[string]*strings.Builder{}
	m.textAgentNames = map[string]string{}
	m.activeTextID = ""

	// Flush reasoning
	for id, buf := range m.reasoningBuffer {
		reasoning := strings.TrimSpace(m.replay.filterReplayText(buf.String()))
		if reasoning == "" {
			m.removeBlock(reasoningBlockKey(id))
		} else {
			m.upsertBlock(reasoningBlockKey(id), eventHeader("REASONING", id), reasoning)
		}
	}
	m.reasoningBuffer = map[string]*strings.Builder{}
	m.activeReasonID = ""

	// Flush thinking
	thinking := strings.TrimSpace(m.thinkingBuffer.String())
	if thinking == "" {
		m.removeBlock("thinking")
	} else {
		m.upsertBlock("thinking", "[THINKING]", thinking)
	}
	m.thinkingBuffer.Reset()

	m.toolBuffers = map[string]*toolCallBuffer{}
}

func (m *model) stopStream() {
	m.runSeq++
	if m.stream != nil {
		m.stream.Close()
		m.stream = nil
	}
	m.starting = false
	m.running = false
	m.finalizeRun()
}

func (m *model) beginRun(history []agui.ChatMessage) uint64 {
	m.runSeq++
	m.starting = true
	m.running = false
	m.replay = newReplayState(history)
	m.resetRunBuffers()
	return m.runSeq
}

func (m *model) resetRunBuffers() {
	m.textBuffers = map[string]*strings.Builder{}
	m.textAgentNames = map[string]string{}
	m.activeTextID = ""
	m.reasoningBuffer = map[string]*strings.Builder{}
	m.activeReasonID = ""
	m.toolBuffers = map[string]*toolCallBuffer{}
	m.thinkingBuffer.Reset()
}

func (m *model) handleLeftClick(screenY int) {
	// Layout: header(1) + viewport + input border(2) + status(1)
	// The viewport area starts after the header line
	viewportTop := 1
	contentLine := screenY - viewportTop + m.viewport.YOffset
	if contentLine >= 0 && contentLine < len(m.lineToBlock) {
		m.selectedBlockIdx = m.lineToBlock[contentLine]
	} else {
		m.selectedBlockIdx = -1
	}
}

func (m *model) copySelectedBlock() {
	if m.selectedBlockIdx < 0 || m.selectedBlockIdx >= len(m.blocks) {
		m.status = "No block selected"
		return
	}
	block := m.blocks[m.selectedBlockIdx]
	text := block.header
	if content := strings.TrimSpace(block.content); content != "" {
		text += "\n" + content
	}
	if err := clipboard.WriteAll(text); err != nil {
		m.status = fmt.Sprintf("Copy failed: %v", err)
		return
	}
	m.status = "Copied to clipboard"
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(m.renderBlocks())
	if m.running || m.selectedBlockIdx >= 0 {
		m.viewport.GotoBottom()
	}
}

func (m *model) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	inputHeight := 3
	headerHeight := 1
	statusHeight := 1
	vpHeight := m.height - headerHeight - inputHeight - statusHeight
	vpHeight = max(vpHeight, 3)
	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
	// Border (2) + horizontal padding (2) + textinput prompt/cursor (3) so the
	// bordered input line fits exactly within the terminal width.
	m.input.Width = max(10, m.width-7)
}

func waitForAsync(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func bridgeStream(runSeq uint64, stream *agui.Stream, out chan<- tea.Msg) {
	go func() {
		events := stream.Events
		errs := stream.Errs

		for events != nil || errs != nil {
			select {
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				out <- aguiEventMsg{runSeq: runSeq, event: event}
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					out <- aguiErrorMsg{runSeq: runSeq, err: err}
				}
			}
		}

		out <- streamDoneMsg{runSeq: runSeq}
	}()
}

func newReplayState(history []agui.ChatMessage) replayState {
	state := replayState{
		historicalIDs:          map[string]struct{}{},
		ignoredTextMessageIDs:  map[string]struct{}{},
		completedTextMessageID: map[string]struct{}{},
	}
	for _, message := range history {
		if agui.NormalizeRole(message.Role) != agui.RoleAssistant {
			continue
		}
		if message.ID != "" {
			state.historicalIDs[normalizeHistoricalTextMessageID(message.ID)] = struct{}{}
		}
		if text := normalizeReplayText(messageText(message.Content)); text != "" {
			state.historicalTexts = append(state.historicalTexts, text)
		}
	}
	return state
}

func (s replayState) filterReplayText(text string) string {
	normalized := normalizeReplayText(text)
	if normalized == "" {
		return ""
	}
	for _, historicalText := range s.historicalTexts {
		if historicalText == "" {
			continue
		}
		if historicalText == normalized || strings.HasPrefix(historicalText, normalized) {
			return ""
		}
		if strings.HasPrefix(normalized, historicalText) {
			remainder := strings.TrimSpace(normalized[len(historicalText):])
			if remainder == "" {
				return ""
			}
			return remainder
		}
	}
	return text
}

func normalizeReplayText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

func normalizeHistoricalTextMessageID(id string) string {
	parts := strings.Split(id, ":")
	if len(parts) >= 3 {
		return strings.Join(parts[2:], ":")
	}
	return id
}

func messageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			obj, ok := item.(map[string]any)
			if !ok || obj["type"] != "text" {
				continue
			}
			if text, ok := obj["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", value)
	}
}

func formatToolCall(buf *toolCallBuffer) string {
	name := strings.TrimSpace(buf.name)
	args := strings.TrimSpace(buf.args.String())
	result := strings.TrimSpace(buf.result)
	var parts []string
	if name != "" {
		parts = append(parts, name)
	}
	if args != "" {
		parts = append(parts, "args: "+args)
	}
	if result != "" {
		parts = append(parts, "result: "+result)
		if buf.isError {
			parts = append(parts, "(error)")
		}
	}
	return strings.Join(parts, " | ")
}

func eventHeader(eventType, id string) string {
	eventType = strings.TrimSpace(eventType)
	id = strings.TrimSpace(id)
	if eventType == "" {
		eventType = "EVENT"
	}
	if id == "" {
		return fmt.Sprintf("[%s]", eventType)
	}
	return fmt.Sprintf("[%s] #%s", eventType, shortID(id))
}

func headerWithAgent(header, agentName string) string {
	agentName = cleanDisplayName(agentName)
	if agentName == "" {
		return header
	}
	return fmt.Sprintf("%s agent: %s", header, agentName)
}

func speakerWithName(speaker, name string) string {
	name = cleanDisplayName(name)
	if name == "" {
		return speaker
	}
	return fmt.Sprintf("%s (%s)", speaker, name)
}

func eventAgentName(raw map[string]any) string {
	return nestedString(raw, "raw_event", "name")
}

func nestedString(raw map[string]any, key, nestedKey string) string {
	if raw == nil {
		return ""
	}
	obj, ok := raw[key].(map[string]any)
	if !ok {
		return ""
	}
	value, ok := obj[nestedKey].(string)
	if !ok {
		return ""
	}
	return value
}

func cleanDisplayName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

func textBlockKey(id string) string {
	return "text:" + id
}

func reasoningBlockKey(id string) string {
	return "reasoning:" + id
}

func toolBlockKey(id string) string {
	return "tool:" + id
}

func keyMatches(msg tea.KeyMsg, key string) bool {
	return msg.String() == key
}

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func valueString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func valueBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func mustThreadID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("thread-%d", time.Now().UnixNano())
	}
	return "thread-" + hex.EncodeToString(buf)
}
