package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"deerflow-tui/internal/agui"
	"deerflow-tui/internal/config"
)

const (
	emptyThreadText         = "(No messages yet. Type to start.)"
	selectionScrollInterval = 50 * time.Millisecond
	terminalPasteProbeWait  = 1500 * time.Millisecond
	terminalPasteBaseWait   = 2 * time.Second
	terminalPasteMaxWait    = 45 * time.Second
)

var (
	readClipboard  = clipboard.ReadAll
	writeClipboard = clipboard.WriteAll
)

type displayBlockKind uint8

const (
	displayBlockPlain displayBlockKind = iota
	displayBlockMarkdown
	displayBlockTool
)

type toolDisplayState uint8

const (
	toolDisplayRunning toolDisplayState = iota
	toolDisplaySucceeded
	toolDisplayFailed
	toolDisplayIncomplete
)

type runStartResultMsg struct {
	runSeq           uint64
	stream           *agui.Stream
	recoveredHistory []agui.ChatMessage
	err              error
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

type threadRecoveredMsg struct {
	runSeq  uint64
	history []agui.ChatMessage
	err     error
}

type clipboardPasteMsg struct {
	probeID uint64
	content string
	err     error
}

type terminalPasteTimeoutMsg struct {
	probeID uint64
	token   uint64
}

type terminalPasteCapture struct {
	id           uint64
	timeoutToken uint64
	expected     string
	expectedSet  bool
	received     string
	processed    int
	buffered     []tea.KeyMsg
}

type pasteBlock struct {
	content string
	lines   int
	start   int
	end     int
}

type selectionScrollTickMsg struct {
	token uint64
}

type textPosition struct {
	line int
	col  int
}

type textSelection struct {
	anchor          textPosition
	cursor          textPosition
	dragging        bool
	active          bool
	scrollDirection int
	scrollToken     uint64
	lastMouseX      int
	lastMouseY      int
}

type displayBlock struct {
	header          string
	content         string
	kind            displayBlockKind
	toolName        string
	toolState       toolDisplayState
	renderedContent string
	renderedWidth   int
	rendered        bool
}

type toolCallBuffer struct {
	id       string
	name     string
	args     strings.Builder
	result   string
	isError  bool
	ended    bool
	complete bool
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
	spinner  spinner.Model
	pastes   []pasteBlock

	windowsPasteCaptureEnabled bool
	terminalPasteSeq           uint64
	terminalPaste              *terminalPasteCapture

	followOutput     bool
	markdownRenderer *glamour.TermRenderer
	markdownWidth    int

	status   string
	threadID string
	starting bool
	running  bool
	runSeq   uint64

	activeRunID        string
	runSession         *runSession
	sessionStore       *runSessionStore
	reconnectAttempt   int
	reconnectDeadline  time.Time
	reconnectScheduled bool

	blocks           []displayBlock
	blockIndexes     map[string]int
	history          []agui.ChatMessage
	interrupts       []agui.Interrupt
	resumeParent     string
	selectedBlockIdx int
	lineToBlock      []int
	renderedLines    []string
	selection        textSelection

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
	vp.KeyMap = viewportNavigationKeyMap()
	vp.SetContent(emptyThreadText)
	toolSpinner := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("6"))),
	)

	m := model{
		cfg:                        cfg,
		client:                     agui.NewClient(cfg.Endpoint, cfg.Headers, cfg.InitialState),
		sessionStore:               newRunSessionStore(cfg.StateDir),
		viewport:                   vp,
		input:                      input,
		spinner:                    toolSpinner,
		windowsPasteCaptureEnabled: runtime.GOOS == "windows",
		followOutput:               true,
		status:                     "Idle",
		threadID:                   mustThreadID(),
		asyncCh:                    make(chan tea.Msg, 512),
		blockIndexes:               map[string]int{},
		replay:                     newReplayState(nil),
		textBuffers:                map[string]*strings.Builder{},
		textAgentNames:             map[string]string{},
		reasoningBuffer:            map[string]*strings.Builder{},
		toolBuffers:                map[string]*toolCallBuffer{},
		selectedBlockIdx:           -1,
	}
	if session, err := m.sessionStore.load(cfg.Endpoint); err == nil && session != nil {
		m.runSession = session
		m.threadID = session.ThreadID
		m.activeRunID = session.RunID
		m.starting = true
		m.running = true
		m.status = "Restoring agent stream..."
		m.reconnectDeadline = time.Now().Add(reconnectWindow)
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.runSession != nil {
		return tea.Batch(textinput.Blink, m.recoverRunCmd())
	}
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	hadRunningTools := m.hasRunningTools()
	previousYOffset := m.viewport.YOffset
	manualScrollUp := false
	manualScrollDown := false

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.clearTextSelection()
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.refreshViewport()

	case tea.KeyMsg:
		if m.terminalPaste != nil {
			m.terminalPaste.buffered = append(m.terminalPaste.buffered, cloneKeyMsg(msg))
			content, replay, resolved := m.evaluateTerminalPaste()
			if !resolved {
				return m, nil
			}
			if content != "" {
				m.handlePastedText(content)
				m.layout()
				m.refreshViewport()
			}
			return replayKeyMessages(m, replay)
		}
		if m.windowsPasteCaptureEnabled && isTerminalPastePrelude(msg) {
			return m, m.startTerminalPasteCapture()
		}
		if key.Matches(msg, m.viewport.KeyMap.Up, m.viewport.KeyMap.PageUp, m.viewport.KeyMap.HalfPageUp) {
			manualScrollUp = true
		}
		if key.Matches(msg, m.viewport.KeyMap.Down, m.viewport.KeyMap.PageDown, m.viewport.KeyMap.HalfPageDown) {
			manualScrollDown = true
		}
		switch {
		case msg.Paste:
			m.handlePastedText(string(msg.Runes))
			m.layout()
			m.refreshViewport()
			return m, tea.Batch(cmds...)
		case keyMatches(msg, "ctrl+v"):
			return m, readClipboardCmd(0)
		case keyMatches(msg, "ctrl+c"):
			m.copyCurrentSelection()
		case keyMatches(msg, "esc"):
			m.clearTextSelection()
			if len(m.pastes) > 0 {
				m.clearPasteBlocks()
				m.status = "Pasted text cleared"
				m.layout()
			}
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
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				manualScrollUp = true
			case tea.MouseButtonWheelDown:
				manualScrollDown = true
			}
		}
		switch {
		case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && !msg.Shift:
			m.startTextSelection(msg.X, msg.Y)
			m.handleLeftClick(msg.Y)
		case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionMotion && m.selection.dragging:
			if selectionCmd := m.dragTextSelection(msg.X, msg.Y); selectionCmd != nil {
				cmds = append(cmds, selectionCmd)
			}
		case msg.Action == tea.MouseActionRelease && m.selection.dragging:
			m.finishTextSelection(msg.X, msg.Y)
		case msg.Button == tea.MouseButtonRight && msg.Action == tea.MouseActionPress && !msg.Shift:
			m.copySelectionAt(msg.Y)
		}

	case selectionScrollTickMsg:
		if selectionCmd := m.handleSelectionScrollTick(msg); selectionCmd != nil {
			cmds = append(cmds, selectionCmd)
		}

	case spinner.TickMsg:
		if m.hasRunningTools() {
			var spinnerCmd tea.Cmd
			m.spinner, spinnerCmd = m.spinner.Update(msg)
			if spinnerCmd != nil {
				cmds = append(cmds, spinnerCmd)
			}
		}

	case runStartResultMsg:
		if msg.runSeq != m.runSeq {
			if msg.stream != nil {
				msg.stream.Close()
			}
			break
		}

		m.reconnectScheduled = false
		if len(msg.recoveredHistory) > 0 {
			m.handleEvent(agui.EventEnvelope{
				Type: "MESSAGES_SNAPSHOT",
				Raw:  map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": chatMessagesToAny(msg.recoveredHistory)},
			})
		}

		if msg.err != nil {
			if cmd, handled := m.handleReconnectableError(msg.err); handled {
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				break
			}
			m.starting = false
			m.running = false
			m.status = fmt.Sprintf("Run error: %v", msg.err)
			m.appendSystemBlock("RUN_ERROR", "", msg.err.Error())
			m.clearRunSession()
			break
		}

		m.stream = msg.stream
		m.activeRunID = msg.stream.RunID
		m.reconnectScheduled = false
		if m.reconnectDeadline.IsZero() {
			m.reconnectAttempt = 0
		}
		m.starting = false
		m.running = true
		m.status = "Running"
		bridgeStream(msg.runSeq, msg.stream, m.asyncCh)
		cmds = append(cmds, waitForAsync(m.asyncCh))

	case aguiEventMsg:
		if msg.runSeq != m.runSeq {
			if m.stream != nil {
				cmds = append(cmds, waitForAsync(m.asyncCh))
			}
			break
		}
		m.reconnectScheduled = false
		m.reconnectAttempt = 0
		m.reconnectDeadline = time.Time{}
		if msg.event.SSEID != "" && m.runSession != nil {
			m.runSession.LastEventID = msg.event.SSEID
			m.runSession.UpdatedAt = time.Now()
			_ = m.sessionStore.save(m.runSession)
		}
		m.handleEvent(msg.event)
		cmds = append(cmds, waitForAsync(m.asyncCh))

	case aguiErrorMsg:
		if msg.runSeq != m.runSeq {
			if m.stream != nil {
				cmds = append(cmds, waitForAsync(m.asyncCh))
			}
			break
		}
		if cmd, handled := m.handleReconnectableError(msg.err); handled {
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else {
			m.starting = false
			m.running = false
			m.status = fmt.Sprintf("Stream error: %v", msg.err)
			m.appendSystemBlock("RUN_ERROR", "", msg.err.Error())
			m.clearRunSession()
		}
		cmds = append(cmds, waitForAsync(m.asyncCh))

	case streamDoneMsg:
		if msg.runSeq != m.runSeq {
			if m.stream != nil {
				cmds = append(cmds, waitForAsync(m.asyncCh))
			}
			break
		}
		m.stream = nil
		if m.running && m.runSession != nil {
			if !m.reconnectScheduled {
				if cmd, handled := m.handleReconnectableError(errors.New("stream ended before terminal event")); handled && cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			break
		}
		m.starting = false
		if strings.HasPrefix(m.status, "Running") || m.status == "Running" {
			m.status = "Idle"
		}

	case threadRecoveredMsg:
		if msg.runSeq != m.runSeq {
			break
		}
		if msg.err != nil {
			m.status = fmt.Sprintf("Replay expired; thread refresh failed: %v", msg.err)
			break
		}
		if len(msg.history) > 0 {
			m.handleEvent(agui.EventEnvelope{
				Type: "MESSAGES_SNAPSHOT",
				Raw:  map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": chatMessagesToAny(msg.history)},
			})
		}
		m.status = "Idle"

	case clipboardPasteMsg:
		if msg.probeID != 0 {
			if m.terminalPaste == nil || m.terminalPaste.id != msg.probeID {
				return m, nil
			}
			if msg.err != nil || msg.content == "" {
				replay := append([]tea.KeyMsg(nil), m.terminalPaste.buffered...)
				m.terminalPaste = nil
				return replayKeyMessages(m, replay)
			}

			m.terminalPaste.expected = normalizePastedText(msg.content)
			m.terminalPaste.expectedSet = true
			m.terminalPaste.timeoutToken++
			content, replay, resolved := m.evaluateTerminalPaste()
			if resolved {
				if content != "" {
					m.handlePastedText(content)
					m.layout()
					m.refreshViewport()
				}
				return replayKeyMessages(m, replay)
			}
			return m, terminalPasteTimeoutCmd(
				msg.probeID,
				m.terminalPaste.timeoutToken,
				terminalPasteCaptureWait(len([]rune(m.terminalPaste.expected))),
			)
		}
		if msg.err != nil {
			m.status = fmt.Sprintf("Paste failed: %v", msg.err)
			return m, nil
		}
		m.handlePastedText(msg.content)
		m.layout()
		m.refreshViewport()
		return m, nil

	case terminalPasteTimeoutMsg:
		if m.terminalPaste == nil || m.terminalPaste.id != msg.probeID || m.terminalPaste.timeoutToken != msg.token {
			return m, nil
		}
		replay := append([]tea.KeyMsg(nil), m.terminalPaste.buffered...)
		m.terminalPaste = nil
		return replayKeyMessages(m, replay)

	case nil:
		// no-op
	}

	var cmd tea.Cmd
	inputBefore := []rune(m.input.Value())
	cursorBefore := m.input.Position()
	m.input, cmd = m.input.Update(msg)
	m.reconcilePasteBlocks(inputBefore, cursorBefore)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	if manualScrollUp && m.viewport.YOffset < previousYOffset {
		m.followOutput = false
	}
	if manualScrollDown && m.viewport.AtBottom() {
		m.followOutput = true
	}
	if !hadRunningTools && m.hasRunningTools() {
		cmds = append(cmds, m.spinner.Tick)
	}

	m.refreshViewport()

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("deerflow-tui") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" - AG-UI Bubble Tea rewrite")

	inputBody := m.input.View()
	inputLine := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1).Render(inputBody)

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

func readClipboardCmd(probeID uint64) tea.Cmd {
	return func() tea.Msg {
		content, err := readClipboard()
		return clipboardPasteMsg{probeID: probeID, content: content, err: err}
	}
}

func (m *model) startTerminalPasteCapture() tea.Cmd {
	m.terminalPasteSeq++
	m.terminalPaste = &terminalPasteCapture{
		id:           m.terminalPasteSeq,
		timeoutToken: 1,
	}
	return tea.Batch(
		readClipboardCmd(m.terminalPaste.id),
		terminalPasteTimeoutCmd(m.terminalPaste.id, m.terminalPaste.timeoutToken, terminalPasteProbeWait),
	)
}

func terminalPasteTimeoutCmd(probeID, token uint64, wait time.Duration) tea.Cmd {
	return tea.Tick(wait, func(time.Time) tea.Msg {
		return terminalPasteTimeoutMsg{probeID: probeID, token: token}
	})
}

func terminalPasteCaptureWait(runes int) time.Duration {
	wait := terminalPasteBaseWait + time.Duration(runes/500)*time.Second
	return min(wait, terminalPasteMaxWait)
}

func isTerminalPastePrelude(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyNull ||
		(msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 0 && !msg.Paste)
}

func cloneKeyMsg(msg tea.KeyMsg) tea.KeyMsg {
	msg.Runes = append([]rune(nil), msg.Runes...)
	return msg
}

func terminalPasteKeyText(msg tea.KeyMsg) (string, bool) {
	if msg.Paste || msg.Alt {
		return "", false
	}
	switch msg.Type {
	case tea.KeyRunes:
		return normalizePastedText(string(msg.Runes)), true
	case tea.KeyEnter, tea.KeyCtrlJ:
		return "\n", true
	case tea.KeyTab:
		return "\t", true
	case tea.KeySpace:
		return " ", true
	default:
		return "", false
	}
}

func (m *model) evaluateTerminalPaste() (content string, replay []tea.KeyMsg, resolved bool) {
	capture := m.terminalPaste
	if capture == nil || !capture.expectedSet {
		return "", nil, false
	}

	for capture.processed < len(capture.buffered) {
		fragment, ok := terminalPasteKeyText(capture.buffered[capture.processed])
		capture.processed++
		if !ok {
			replay = append([]tea.KeyMsg(nil), capture.buffered...)
			m.terminalPaste = nil
			return "", replay, true
		}
		capture.received += fragment
		if !strings.HasPrefix(capture.expected, capture.received) {
			replay = append([]tea.KeyMsg(nil), capture.buffered...)
			m.terminalPaste = nil
			return "", replay, true
		}
		if capture.received == capture.expected {
			content = capture.expected
			replay = append([]tea.KeyMsg(nil), capture.buffered[capture.processed:]...)
			m.terminalPaste = nil
			return content, replay, true
		}
	}
	return "", nil, false
}

func replayKeyMessages(m model, messages []tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0, len(messages))
	for _, msg := range messages {
		updated, cmd := m.Update(msg)
		m = updated.(model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func normalizePastedText(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(content, "\r", "\n")
}

func (m *model) handlePastedText(content string) {
	content = normalizePastedText(content)
	if content == "" {
		m.status = "Clipboard is empty"
		return
	}
	if m.input.CharLimit > 0 && m.draftRuneCount()+len([]rune(content)) > m.input.CharLimit {
		m.status = fmt.Sprintf("Paste too large (maximum draft length: %d characters)", m.input.CharLimit)
		return
	}
	if !strings.Contains(content, "\n") {
		m.insertInputText(content)
		m.status = "Pasted from clipboard"
		return
	}
	lines := pastedLineCount(content)
	label := pasteBlockLabel(lines)
	start := m.snapPasteCursor(m.input.Position(), 1)
	m.input.SetCursor(start)
	m.insertInputText(label)
	m.pastes = append(m.pastes, pasteBlock{
		content: content,
		lines:   lines,
		start:   start,
		end:     start + len([]rune(label)),
	})
	m.sortPasteBlocks()
	m.status = "Multiline paste attached; press Enter to send"
}

func (m *model) insertInputText(text string) {
	value := []rune(m.input.Value())
	position := m.snapPasteCursor(min(m.input.Position(), len(value)), 1)
	inserted := []rune(text)
	combined := make([]rune, 0, len(value)+len(inserted))
	combined = append(combined, value[:position]...)
	combined = append(combined, inserted...)
	combined = append(combined, value[position:]...)
	for i := range m.pastes {
		if m.pastes[i].start >= position {
			m.pastes[i].start += len(inserted)
			m.pastes[i].end += len(inserted)
		}
	}
	m.input.SetValue(string(combined))
	m.input.SetCursor(position + len(inserted))
}

func (m *model) draftRuneCount() int {
	total := len([]rune(m.input.Value()))
	for _, paste := range m.pastes {
		total += len([]rune(paste.content)) - (paste.end - paste.start)
	}
	return total
}

func pastedLineCount(content string) int {
	content = strings.TrimSuffix(content, "\n")
	return strings.Count(content, "\n") + 1
}

func pasteBlockLabel(lines int) string {
	return fmt.Sprintf("[paste %d lines]", lines)
}

func (m *model) pasteSummary() string {
	if len(m.pastes) == 0 {
		return ""
	}
	totalLines := 0
	for _, paste := range m.pastes {
		totalLines += paste.lines
	}
	if len(m.pastes) == 1 {
		return fmt.Sprintf("[paste %d lines]", totalLines)
	}
	return fmt.Sprintf("[%d pastes · %d lines]", len(m.pastes), totalLines)
}

func (m *model) sortPasteBlocks() {
	sort.SliceStable(m.pastes, func(i, j int) bool {
		return m.pastes[i].start < m.pastes[j].start
	})
}

func (m *model) snapPasteCursor(position, direction int) int {
	for _, paste := range m.pastes {
		if position > paste.start && position < paste.end {
			if direction < 0 {
				return paste.start
			}
			return paste.end
		}
	}
	return position
}

func (m *model) clearPasteBlocks() {
	if len(m.pastes) == 0 {
		return
	}
	m.sortPasteBlocks()
	value := []rune(m.input.Value())
	cursor := m.input.Position()
	for i := len(m.pastes) - 1; i >= 0; i-- {
		paste := m.pastes[i]
		if paste.start < 0 || paste.end > len(value) || paste.start > paste.end {
			continue
		}
		value = append(value[:paste.start], value[paste.end:]...)
		size := paste.end - paste.start
		switch {
		case cursor >= paste.end:
			cursor -= size
		case cursor > paste.start:
			cursor = paste.start
		}
	}
	m.pastes = nil
	m.input.SetValue(string(value))
	m.input.SetCursor(cursor)
}

func (m *model) reconcilePasteBlocks(before []rune, cursorBefore int) {
	if len(m.pastes) == 0 {
		return
	}
	after := []rune(m.input.Value())
	start, beforeEnd, afterEnd, changed := inputEdit(before, after)
	if !changed {
		direction := m.input.Position() - cursorBefore
		m.input.SetCursor(m.snapPasteCursor(m.input.Position(), direction))
		return
	}

	inserted := append([]rune(nil), after[start:afterEnd]...)
	if start == beforeEnd {
		for _, paste := range m.pastes {
			if start > paste.start && start < paste.end {
				m.input.SetValue(string(before))
				m.input.SetCursor(paste.end)
				m.insertInputText(string(inserted))
				return
			}
		}
	}

	affected := make(map[int]struct{})
	expandedStart := start
	expandedEnd := beforeEnd
	for i, paste := range m.pastes {
		if start < paste.end && beforeEnd > paste.start {
			affected[i] = struct{}{}
			expandedStart = min(expandedStart, paste.start)
			expandedEnd = max(expandedEnd, paste.end)
		}
	}

	if len(affected) > 0 {
		rebuilt := make([]rune, 0, len(before)-(expandedEnd-expandedStart)+len(inserted))
		rebuilt = append(rebuilt, before[:expandedStart]...)
		rebuilt = append(rebuilt, inserted...)
		rebuilt = append(rebuilt, before[expandedEnd:]...)
		delta := len(inserted) - (expandedEnd - expandedStart)
		kept := m.pastes[:0]
		for i, paste := range m.pastes {
			if _, remove := affected[i]; remove {
				continue
			}
			if paste.start >= expandedEnd {
				paste.start += delta
				paste.end += delta
			}
			kept = append(kept, paste)
		}
		m.pastes = kept
		m.input.SetValue(string(rebuilt))
		m.input.SetCursor(expandedStart + len(inserted))
		return
	}

	delta := (afterEnd - start) - (beforeEnd - start)
	for i := range m.pastes {
		if beforeEnd <= m.pastes[i].start {
			m.pastes[i].start += delta
			m.pastes[i].end += delta
		}
	}
	direction := m.input.Position() - cursorBefore
	m.input.SetCursor(m.snapPasteCursor(m.input.Position(), direction))
}

func inputEdit(before, after []rune) (start, beforeEnd, afterEnd int, changed bool) {
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	if start == len(before) && start == len(after) {
		return start, start, start, false
	}
	beforeEnd = len(before)
	afterEnd = len(after)
	for beforeEnd > start && afterEnd > start && before[beforeEnd-1] == after[afterEnd-1] {
		beforeEnd--
		afterEnd--
	}
	return start, beforeEnd, afterEnd, true
}

func (m *model) composedInput() string {
	value := []rune(m.input.Value())
	if len(m.pastes) == 0 {
		return strings.TrimSpace(string(value))
	}
	m.sortPasteBlocks()
	var composed strings.Builder
	last := 0
	for _, paste := range m.pastes {
		if paste.start < last || paste.start < 0 || paste.end > len(value) || paste.start > paste.end {
			continue
		}
		composed.WriteString(string(value[last:paste.start]))
		composed.WriteString(paste.content)
		last = paste.end
	}
	composed.WriteString(string(value[last:]))
	return strings.TrimSpace(composed.String())
}

func (m *model) handleSubmit() tea.Cmd {
	hasPastes := len(m.pastes) > 0
	text := m.composedInput()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	m.input.SetValue("")
	m.pastes = nil
	m.layout()
	m.followOutput = true

	command := ""
	if !hasPastes {
		command = strings.ToLower(text)
	}
	switch command {
	case "/exit", "/quit":
		m.stopStream()
		return tea.Quit
	case "/cancel":
		m.cancelServerRun()
		m.stopStream()
		m.clearRunSession()
		m.clearInterrupts()
		m.status = "Idle (cancelled)"
		return nil
	case "/new":
		m.cancelServerRun()
		m.stopStream()
		m.clearRunSession()
		m.threadID = mustThreadID()
		m.history = nil
		m.clearInterrupts()
		m.blocks = nil
		m.blockIndexes = map[string]int{}
		m.replay = newReplayState(nil)
		m.selectedBlockIdx = -1
		m.lineToBlock = nil
		m.clearTextSelection()
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
		m.cancelServerRun()
		m.stopStream()
		m.clearRunSession()
		m.status = "Restarting run..."
	}

	m.appendChatBlock("You", "", text)
	m.history = append(m.history, agui.ChatMessage{Role: agui.RoleUser, Content: text})

	history := append([]agui.ChatMessage(nil), m.history...)
	threadID := m.threadID
	runID, err := agui.NewRunID()
	if err != nil {
		m.status = fmt.Sprintf("Run error: %v", err)
		return nil
	}
	runSeq := m.beginRun(history)
	m.setRunSession(&runSession{Endpoint: m.cfg.Endpoint, ThreadID: threadID, RunID: runID, UpdatedAt: time.Now()})
	m.status = "Starting run..."

	return func() tea.Msg {
		stream, err := m.client.ConnectRun(context.Background(), agui.RunRequest{RunID: runID, ThreadID: threadID, History: history})
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
	runID, err := agui.NewRunID()
	if err != nil {
		m.status = fmt.Sprintf("Resume error: %v", err)
		return nil
	}
	runSeq := m.beginRun(history)
	m.setRunSession(&runSession{Endpoint: m.cfg.Endpoint, ThreadID: threadID, RunID: runID, UpdatedAt: time.Now()})
	m.status = "Resuming run..."
	m.clearInterrupts()

	return func() tea.Msg {
		stream, err := m.client.ConnectRun(context.Background(), agui.RunRequest{RunID: runID, ThreadID: threadID, History: history, Resume: responses})
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
		m.clearRunSession()
		if interrupts := agui.InterruptsFromRunFinished(event); len(interrupts) > 0 {
			m.running = false
			m.status = "Requires interrupt response"
			m.interrupts = interrupts
			m.appendInterruptBlock(interrupts)
			return
		}
		m.running = false
		m.status = "Idle"
		m.appendSystemBlock("RUN_FINISHED", valueString(event.Raw, "runId"), "")

	case "RUN_CANCELLED":
		m.clearRunSession()
		m.running = false
		m.status = "Cancelled"
		m.finalizeRun()
		m.appendSystemBlock("RUN_CANCELLED", valueString(event.Raw, "runId"), "")

	case "RUN_ERROR":
		m.clearRunSession()
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

	case "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CHUNK":
		id := m.resolveTextID(event.MessageID, false)
		if m.shouldIgnoreTextContent(id) {
			return
		}
		m.recordTextAgentName(id, event)
		if event.Delta == "" {
			if buf, ok := m.textBuffers[id]; ok {
				m.renderStreamingText(id, buf.String())
			}
			return
		}
		buf := m.ensureTextBuffer(id)
		buf.WriteString(event.Delta)
		m.renderStreamingText(id, buf.String())

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
		m.upsertMarkdownBlock(textBlockKey(id), m.textMessageHeader(id), filtered)
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
		toolID := m.resolveToolID(eventToolCallID(event))
		name := eventToolCallName(event)
		m.toolBuffers[toolID] = &toolCallBuffer{id: toolID, name: name}
		m.upsertToolBlock(toolID, name, toolDisplayRunning)
		m.recordToolCallStart(toolID, name)

	case "TOOL_CALL_ARGS", "TOOL_CALL_CHUNK":
		toolID := m.resolveToolID(eventToolCallID(event))
		buf, ok := m.toolBuffers[toolID]
		if !ok {
			buf = &toolCallBuffer{id: toolID, name: eventToolCallName(event)}
			m.toolBuffers[toolID] = buf
		}
		if buf.name == "" {
			buf.name = eventToolCallName(event)
		}
		buf.args.WriteString(event.Delta)
		m.upsertToolBlock(toolID, buf.name, toolDisplayRunning)
		m.updateToolCallArgs(toolID, buf.name, buf.args.String())

	case "TOOL_CALL_END":
		toolID := m.resolveToolID(eventToolCallID(event))
		buf, ok := m.toolBuffers[toolID]
		if !ok {
			buf = &toolCallBuffer{id: toolID, name: eventToolCallName(event)}
			m.toolBuffers[toolID] = buf
		}
		buf.ended = true
		m.upsertToolBlock(toolID, buf.name, toolDisplayRunning)

	case "TOOL_CALL_RESULT":
		toolID := m.resolveToolID(eventToolCallID(event))
		content := valueString(event.Raw, "content")
		if content == "" {
			content = event.Content
		}
		if content == "" {
			content = compactJSON(event.Raw)
		}
		buf, ok := m.toolBuffers[toolID]
		if !ok {
			buf = &toolCallBuffer{id: toolID, name: eventToolCallName(event)}
			m.toolBuffers[toolID] = buf
		}
		if buf.name == "" {
			buf.name = eventToolCallName(event)
		}
		buf.result = content
		buf.isError = valueBool(event.Raw, "isError") || valueString(event.Raw, "role") != "tool" && valueString(event.Raw, "role") != ""
		buf.complete = true
		state := toolDisplaySucceeded
		if buf.isError {
			state = toolDisplayFailed
		}
		m.upsertToolBlock(toolID, buf.name, state)
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
	m.clearTextSelection()
	m.replay.addHistoricalMessages(messages)
	for id := range m.textBuffers {
		if _, ok := m.replay.historicalIDs[normalizeHistoricalTextMessageID(id)]; !ok {
			continue
		}
		m.replay.ignoredTextMessageIDs[id] = struct{}{}
		delete(m.textBuffers, id)
		delete(m.textAgentNames, id)
	}
	m.history = messages
	m.blocks = nil
	m.blockIndexes = map[string]int{}
	m.toolBuffers = map[string]*toolCallBuffer{}
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
			m.appendAssistantBlock(speakerWithName("Assistant", message.Name), message.ID, text)
		}
		for _, call := range message.ToolCalls {
			buf := &toolCallBuffer{id: call.ID, name: call.Function.Name}
			buf.args.WriteString(call.Function.Arguments)
			m.toolBuffers[call.ID] = buf
			m.upsertToolBlock(call.ID, call.Function.Name, toolDisplayRunning)
		}
	case agui.RoleTool:
		toolID := strings.TrimSpace(message.ToolCallID)
		if toolID == "" {
			toolID = m.resolveToolID("")
		}
		buf, ok := m.toolBuffers[toolID]
		if !ok {
			buf = &toolCallBuffer{id: toolID, name: "tool"}
			m.toolBuffers[toolID] = buf
		}
		buf.result = messageText(message.Content)
		buf.isError = strings.TrimSpace(message.Error) != ""
		buf.complete = true
		state := toolDisplaySucceeded
		if buf.isError {
			state = toolDisplayFailed
		}
		m.upsertToolBlock(toolID, buf.name, state)
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
	m.appendBlock(displayBlock{header: eventHeader(speaker, id), content: strings.TrimSpace(text)})
}

func (m *model) appendAssistantBlock(speaker, id, text string) {
	m.appendBlock(displayBlock{header: eventHeader(speaker, id), content: strings.TrimSpace(text), kind: displayBlockMarkdown})
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
	return eventHeader(speakerWithName("Assistant", m.textAgentNames[id]), id)
}

func (m *model) renderStreamingText(id, text string) {
	filtered := m.replay.filterReplayText(text)
	if filtered == "" {
		m.removeBlock(textBlockKey(id))
		return
	}
	m.upsertMarkdownBlock(textBlockKey(id), m.textMessageHeader(id), filtered)
}

func (m *model) appendBlock(block displayBlock) {
	m.blocks = append(m.blocks, block)
}

func (m *model) upsertBlock(key, header, content string) {
	m.upsertDisplayBlock(key, displayBlock{header: header, content: strings.TrimSpace(content)})
}

func (m *model) upsertMarkdownBlock(key, header, content string) {
	m.upsertDisplayBlock(key, displayBlock{header: header, content: strings.TrimSpace(content), kind: displayBlockMarkdown})
}

func (m *model) upsertToolBlock(toolID, name string, state toolDisplayState) {
	key := toolBlockKey(toolID)
	name = cleanDisplayName(name)
	if idx, ok := m.blockIndexes[key]; ok && idx >= 0 && idx < len(m.blocks) && name == "" {
		name = m.blocks[idx].toolName
	}
	if name == "" {
		name = "tool"
	}
	m.upsertDisplayBlock(key, displayBlock{kind: displayBlockTool, toolName: name, toolState: state})
}

func (m *model) upsertDisplayBlock(key string, block displayBlock) {
	if idx, ok := m.blockIndexes[key]; ok && idx >= 0 && idx < len(m.blocks) {
		m.blocks[idx] = block
		return
	}
	m.blockIndexes[key] = len(m.blocks)
	m.blocks = append(m.blocks, block)
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
	m.renderedLines = nil
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
			rendered := b.String()
			m.renderedLines = strings.Split(rendered, "\n")
			return rendered
		}
		m.renderedLines = []string{emptyThreadText}
		return emptyThreadText
	}

	var out []string
	for i, block := range m.blocks {
		prefix := "  "
		if i == m.selectedBlockIdx {
			prefix = "▸ "
		}
		m.lineToBlock = append(m.lineToBlock, i)
		header := block.header
		if block.kind == displayBlockTool {
			header = m.renderToolStatus(block)
		}
		out = append(out, prefix+header)
		content := strings.TrimSpace(block.content)
		if content != "" {
			wrapWidth := m.viewport.Width - 4
			if wrapWidth < 1 {
				wrapWidth = 1
			}
			wrapped := ""
			if block.kind == displayBlockMarkdown {
				if block.rendered && block.renderedWidth == wrapWidth {
					wrapped = block.renderedContent
				} else {
					wrapped = m.renderMarkdown(content, wrapWidth)
					m.blocks[i].renderedContent = wrapped
					m.blocks[i].renderedWidth = wrapWidth
					m.blocks[i].rendered = true
				}
			} else {
				wrapped = lipgloss.NewStyle().Width(wrapWidth).Render(content)
			}
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

	m.renderedLines = append([]string(nil), out...)
	m.applyTextSelection(out)
	return strings.Join(out, "\n")
}

func (m *model) renderMarkdown(content string, width int) string {
	if m.markdownRenderer == nil || m.markdownWidth != width {
		markdownStyle := styles.DarkStyleConfig
		markdownStyle.H1.Prefix = ""
		markdownStyle.H1.Suffix = ""
		markdownStyle.H2.Prefix = ""
		markdownStyle.H2.Suffix = ""
		markdownStyle.H3.Prefix = ""
		markdownStyle.H3.Suffix = ""
		markdownStyle.H4.Prefix = ""
		markdownStyle.H4.Suffix = ""
		markdownStyle.H5.Prefix = ""
		markdownStyle.H5.Suffix = ""
		markdownStyle.H6.Prefix = ""
		markdownStyle.H6.Suffix = ""
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(markdownStyle),
			glamour.WithWordWrap(width),
			glamour.WithTableWrap(true),
		)
		if err != nil {
			return lipgloss.NewStyle().Width(width).Render(content)
		}
		m.markdownRenderer = renderer
		m.markdownWidth = width
	}

	rendered, err := m.markdownRenderer.Render(content)
	if err != nil {
		return lipgloss.NewStyle().Width(width).Render(content)
	}
	return strings.Trim(rendered, "\r\n")
}

func (m *model) renderToolStatus(block displayBlock) string {
	name := cleanDisplayName(block.toolName)
	if name == "" {
		name = "tool"
	}
	switch block.toolState {
	case toolDisplaySucceeded:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("✓ " + name)
	case toolDisplayFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("✗ " + name)
	case toolDisplayIncomplete:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("! " + name)
	default:
		return m.spinner.View() + " " + name
	}
}

func toolStatusText(block displayBlock) string {
	name := cleanDisplayName(block.toolName)
	if name == "" {
		name = "tool"
	}
	marker := "…"
	switch block.toolState {
	case toolDisplaySucceeded:
		marker = "✓"
	case toolDisplayFailed:
		marker = "✗"
	case toolDisplayIncomplete:
		marker = "!"
	}
	return marker + " " + name
}

func (m *model) hasRunningTools() bool {
	for _, block := range m.blocks {
		if block.kind == displayBlockTool && block.toolState == toolDisplayRunning {
			return true
		}
	}
	return false
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
		m.upsertMarkdownBlock(textBlockKey(id), m.textMessageHeader(id), text)
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

	for toolID, buf := range m.toolBuffers {
		if !buf.complete {
			m.upsertToolBlock(toolID, buf.name, toolDisplayIncomplete)
		}
	}
	m.toolBuffers = map[string]*toolCallBuffer{}
}

func (m *model) setRunSession(session *runSession) {
	m.runSession = session
	m.activeRunID = session.RunID
	m.reconnectAttempt = 0
	m.reconnectDeadline = time.Time{}
	m.reconnectScheduled = false
	_ = m.sessionStore.save(session)
}

func (m *model) clearRunSession() {
	m.runSession = nil
	m.activeRunID = ""
	m.reconnectAttempt = 0
	m.reconnectDeadline = time.Time{}
	m.reconnectScheduled = false
	_ = m.sessionStore.clear()
}

func (m model) recoverRunCmd() tea.Cmd {
	session := *m.runSession
	return func() tea.Msg {
		history, _ := m.client.GetThreadMessages(context.Background(), session.ThreadID)
		stream, err := m.client.ConnectRun(context.Background(), agui.RunRequest{
			RunID:       session.RunID,
			ThreadID:    session.ThreadID,
			LastEventID: initialReplayID,
		})
		return runStartResultMsg{runSeq: m.runSeq, stream: stream, recoveredHistory: history, err: err}
	}
}

func (m *model) handleReconnectableError(err error) (tea.Cmd, bool) {
	if m.runSession == nil {
		return nil, false
	}
	var httpErr *agui.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == 410 {
			m.starting = false
			m.running = false
			m.status = "Replay expired; loading the latest thread checkpoint..."
			m.appendSystemBlock("REPLAY_EXPIRED", "", "The saved stream can no longer be replayed.")
			threadID := m.runSession.ThreadID
			runSeq := m.runSeq
			m.clearRunSession()
			return func() tea.Msg {
				history, fetchErr := m.client.GetThreadMessages(context.Background(), threadID)
				return threadRecoveredMsg{runSeq: runSeq, history: history, err: fetchErr}
			}, true
		}
		if !httpErr.Retryable() {
			return nil, false
		}
	}
	if m.reconnectScheduled {
		return nil, true
	}
	now := time.Now()
	if m.reconnectDeadline.IsZero() {
		m.reconnectDeadline = now.Add(reconnectWindow)
	}
	if !now.Before(m.reconnectDeadline) {
		return nil, false
	}

	m.reconnectAttempt++
	m.reconnectScheduled = true
	m.starting = true
	m.running = true
	if m.stream != nil {
		m.stream.Close()
		m.stream = nil
	}
	m.runSeq++
	remaining := time.Until(m.reconnectDeadline).Round(time.Second)
	if remaining < time.Second {
		remaining = time.Second
	}
	delay := reconnectDelay(m.reconnectAttempt)
	if delay > time.Until(m.reconnectDeadline) {
		delay = time.Until(m.reconnectDeadline)
	}
	m.status = fmt.Sprintf("Reconnecting stream... (%s remaining)", remaining)
	m.runSession.UpdatedAt = now
	_ = m.sessionStore.save(m.runSession)

	session := *m.runSession
	history := append([]agui.ChatMessage(nil), m.history...)
	runSeq := m.runSeq
	lastEventID := session.LastEventID
	if lastEventID == "" {
		lastEventID = initialReplayID
	}
	return func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		stream, connectErr := m.client.ConnectRun(context.Background(), agui.RunRequest{
			RunID:       session.RunID,
			ThreadID:    session.ThreadID,
			History:     history,
			LastEventID: lastEventID,
		})
		return runStartResultMsg{runSeq: runSeq, stream: stream, err: connectErr}
	}, true
}

func (m *model) stopStream() {
	m.runSeq++
	if m.stream != nil {
		m.stream.Close()
		m.stream = nil
	}
	m.starting = false
	m.running = false
	m.activeRunID = ""
	m.finalizeRun()
}

func (m *model) cancelServerRun() {
	if m.activeRunID == "" {
		return
	}
	go func(runID string) {
		if err := m.client.CancelRun(context.Background(), runID); err != nil {
			m.asyncCh <- aguiErrorMsg{runSeq: m.runSeq, err: fmt.Errorf("server cancel failed: %w", err)}
		}
	}(m.activeRunID)
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

func (m *model) startTextSelection(screenX, screenY int) {
	if !m.screenYInViewport(screenY) {
		m.clearTextSelection()
		return
	}
	position, ok := m.textPositionAtScreen(screenX, screenY)
	if !ok {
		m.clearTextSelection()
		return
	}
	token := m.selection.scrollToken + 1
	m.selection = textSelection{
		anchor:      position,
		cursor:      position,
		dragging:    true,
		scrollToken: token,
		lastMouseX:  screenX,
		lastMouseY:  screenY,
	}
}

func (m *model) dragTextSelection(screenX, screenY int) tea.Cmd {
	m.selection.lastMouseX = screenX
	m.selection.lastMouseY = screenY
	direction := m.selectionScrollDirection(screenY)
	if direction != 0 && !m.scrollTextSelection(direction) {
		direction = 0
	}
	if position, ok := m.textPositionAtScreen(screenX, screenY); ok {
		m.selection.cursor = position
		m.selection.active = position != m.selection.anchor
		if m.selection.active {
			m.selectedBlockIdx = -1
		}
	}
	return m.setSelectionScrollDirection(direction)
}

func (m *model) finishTextSelection(screenX, screenY int) {
	if position, ok := m.textPositionAtScreen(screenX, screenY); ok {
		m.selection.cursor = position
		m.selection.active = position != m.selection.anchor
	}
	m.selection.dragging = false
	m.setSelectionScrollDirection(0)
	if m.selection.active {
		m.selectedBlockIdx = -1
		m.status = "Text selected; right-click or Ctrl+C to copy"
	}
}

func (m *model) handleSelectionScrollTick(msg selectionScrollTickMsg) tea.Cmd {
	if msg.token != m.selection.scrollToken || !m.selection.dragging || m.selection.scrollDirection == 0 {
		return nil
	}
	if !m.scrollTextSelection(m.selection.scrollDirection) {
		m.setSelectionScrollDirection(0)
		return nil
	}
	if position, ok := m.textPositionAtScreen(m.selection.lastMouseX, m.selection.lastMouseY); ok {
		m.selection.cursor = position
		m.selection.active = position != m.selection.anchor
		if m.selection.active {
			m.selectedBlockIdx = -1
		}
	}
	return selectionScrollCmd(msg.token)
}

func selectionScrollCmd(token uint64) tea.Cmd {
	return tea.Tick(selectionScrollInterval, func(time.Time) tea.Msg {
		return selectionScrollTickMsg{token: token}
	})
}

func (m *model) setSelectionScrollDirection(direction int) tea.Cmd {
	if direction == m.selection.scrollDirection {
		return nil
	}
	m.selection.scrollToken++
	m.selection.scrollDirection = direction
	if direction == 0 {
		return nil
	}
	return selectionScrollCmd(m.selection.scrollToken)
}

func (m *model) scrollTextSelection(direction int) bool {
	previousYOffset := m.viewport.YOffset
	if direction < 0 {
		m.viewport.ScrollUp(1)
	} else {
		m.viewport.ScrollDown(1)
	}
	if m.viewport.YOffset == previousYOffset {
		return false
	}
	m.followOutput = false
	return true
}

func (m *model) selectionScrollDirection(screenY int) int {
	viewportTop := 1
	viewportBottom := viewportTop + m.viewport.Height - 1
	switch {
	case screenY <= viewportTop:
		return -1
	case screenY >= viewportBottom:
		return 1
	default:
		return 0
	}
}

func (m *model) screenYInViewport(screenY int) bool {
	viewportTop := 1
	return screenY >= viewportTop && screenY < viewportTop+m.viewport.Height
}

func (m *model) textPositionAtScreen(screenX, screenY int) (textPosition, bool) {
	if len(m.renderedLines) == 0 {
		return textPosition{}, false
	}
	viewportTop := 1
	viewportBottom := viewportTop + m.viewport.Height - 1
	if screenY < viewportTop {
		screenY = viewportTop
	}
	if screenY > viewportBottom {
		screenY = viewportBottom
	}
	line := m.viewport.YOffset + screenY - viewportTop
	if line < 0 {
		line = 0
	}
	if line >= len(m.renderedLines) {
		line = len(m.renderedLines) - 1
	}
	width := ansi.StringWidth(m.renderedLines[line])
	if screenX < 0 {
		screenX = 0
	}
	if screenX > width {
		screenX = width
	}
	return textPosition{line: line, col: screenX}, true
}

func (m *model) clearTextSelection() {
	token := m.selection.scrollToken + 1
	m.selection = textSelection{scrollToken: token}
}

func (m *model) selectionBounds() (textPosition, textPosition, bool) {
	if !m.selection.active {
		return textPosition{}, textPosition{}, false
	}
	start := m.selection.anchor
	end := m.selection.cursor
	if end.line < start.line || end.line == start.line && end.col < start.col {
		start, end = end, start
	}
	if start.line < 0 || end.line >= len(m.renderedLines) {
		return textPosition{}, textPosition{}, false
	}
	return start, end, true
}

func (m *model) applyTextSelection(lines []string) {
	start, end, ok := m.selectionBounds()
	if !ok {
		return
	}
	for lineIndex := start.line; lineIndex <= end.line && lineIndex < len(lines); lineIndex++ {
		lineWidth := ansi.StringWidth(lines[lineIndex])
		left := 0
		right := lineWidth
		if lineIndex == start.line {
			left = min(start.col, lineWidth)
		}
		if lineIndex == end.line {
			right = min(end.col+1, lineWidth)
		}
		if right <= left {
			continue
		}
		before := ansi.Cut(lines[lineIndex], 0, left)
		// Strip nested Markdown styles inside the selected span so their reset
		// sequences cannot cancel the reverse-video selection highlight midway.
		selected := ansi.Strip(ansi.Cut(lines[lineIndex], left, right))
		after := ansi.Cut(lines[lineIndex], right, lineWidth)
		lines[lineIndex] = before + "\x1b[7m" + selected + "\x1b[27m" + after
	}
}

func (m *model) selectedText() string {
	start, end, ok := m.selectionBounds()
	if !ok {
		return ""
	}
	parts := make([]string, 0, end.line-start.line+1)
	for lineIndex := start.line; lineIndex <= end.line; lineIndex++ {
		line := ansi.Strip(m.renderedLines[lineIndex])
		lineWidth := ansi.StringWidth(line)
		left := 0
		right := lineWidth
		if lineIndex == start.line {
			left = min(start.col, lineWidth)
		}
		if lineIndex == end.line {
			right = min(end.col+1, lineWidth)
		}
		part := ""
		if right > left {
			part = ansi.Cut(line, left, right)
		}
		parts = append(parts, strings.TrimRight(part, " "))
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func (m *model) copyCurrentSelection() {
	if m.selection.active {
		m.copyTextSelection()
		return
	}
	m.copySelectedBlock()
}

func (m *model) copySelectionAt(screenY int) {
	if m.selection.active {
		m.copyTextSelection()
		return
	}
	m.handleLeftClick(screenY)
	m.copySelectedBlock()
}

func (m *model) copyTextSelection() {
	text := m.selectedText()
	if text == "" {
		m.status = "No text selected"
		return
	}
	if err := writeClipboard(text); err != nil {
		m.status = fmt.Sprintf("Copy failed: %v", err)
		return
	}
	m.status = "Selected text copied to clipboard"
}

func (m *model) copySelectedBlock() {
	if m.selectedBlockIdx < 0 || m.selectedBlockIdx >= len(m.blocks) {
		m.status = "No block selected"
		return
	}
	block := m.blocks[m.selectedBlockIdx]
	text := block.header
	if block.kind == displayBlockTool {
		text = toolStatusText(block)
	}
	if content := strings.TrimSpace(block.content); content != "" {
		text += "\n" + content
	}
	if err := writeClipboard(text); err != nil {
		m.status = fmt.Sprintf("Copy failed: %v", err)
		return
	}
	m.status = "Copied to clipboard"
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(m.renderBlocks())
	if m.followOutput {
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

func viewportNavigationKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		Down:     key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		Up:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
	}
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

func chatMessagesToAny(messages []agui.ChatMessage) []any {
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil
	}
	var converted []any
	if err := json.Unmarshal(raw, &converted); err != nil {
		return nil
	}
	return converted
}

func newReplayState(history []agui.ChatMessage) replayState {
	state := replayState{
		historicalIDs:          map[string]struct{}{},
		ignoredTextMessageIDs:  map[string]struct{}{},
		completedTextMessageID: map[string]struct{}{},
	}
	state.addHistoricalMessages(history)
	return state
}

func (s *replayState) addHistoricalMessages(messages []agui.ChatMessage) {
	if s.historicalIDs == nil {
		s.historicalIDs = map[string]struct{}{}
	}
	if s.ignoredTextMessageIDs == nil {
		s.ignoredTextMessageIDs = map[string]struct{}{}
	}
	if s.completedTextMessageID == nil {
		s.completedTextMessageID = map[string]struct{}{}
	}
	knownTexts := make(map[string]struct{}, len(s.historicalTexts))
	for _, text := range s.historicalTexts {
		knownTexts[text] = struct{}{}
	}
	for _, message := range messages {
		if agui.NormalizeRole(message.Role) != agui.RoleAssistant {
			continue
		}
		if message.ID != "" {
			s.historicalIDs[normalizeHistoricalTextMessageID(message.ID)] = struct{}{}
		}
		if text := normalizeReplayText(messageText(message.Content)); text != "" {
			if _, exists := knownTexts[text]; exists {
				continue
			}
			s.historicalTexts = append(s.historicalTexts, text)
			knownTexts[text] = struct{}{}
		}
	}
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

func eventToolCallID(event agui.EventEnvelope) string {
	if id := strings.TrimSpace(event.ToolCallID); id != "" {
		return id
	}
	return valueString(event.Raw, "toolCallId")
}

func eventToolCallName(event agui.EventEnvelope) string {
	if name := cleanDisplayName(event.ToolCallName); name != "" {
		return name
	}
	return cleanDisplayName(valueString(event.Raw, "toolCallName"))
}

func eventHeader(eventType, _ string) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		eventType = "EVENT"
	}
	return fmt.Sprintf("[%s]", eventType)
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
