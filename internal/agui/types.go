package agui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	RoleDeveloper = "developer"
	RoleSystem    = "system"
	RoleAssistant = "assistant"
	RoleUser      = "user"
	RoleTool      = "tool"
	RoleActivity  = "activity"
	RoleReasoning = "reasoning"
)

var allowedRoles = map[string]struct{}{
	RoleDeveloper: {},
	RoleSystem:    {},
	RoleAssistant: {},
	RoleUser:      {},
	RoleTool:      {},
	RoleActivity:  {},
	RoleReasoning: {},
}

var knownRoleRemaps = map[string]string{
	"ai":       RoleAssistant,
	"human":    RoleUser,
	"function": RoleTool,
}

type EventEnvelope struct {
	Type            string
	Raw             map[string]any
	MessageID       string
	Delta           string
	ToolCallID      string
	ToolCallName    string
	ParentMessageID string
	Content         string
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ID         string     `json:"id,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type Interrupt struct {
	ID             string         `json:"id"`
	Reason         string         `json:"reason"`
	Message        string         `json:"message,omitempty"`
	ToolCallID     string         `json:"toolCallId,omitempty"`
	ExpiresAt      string         `json:"expiresAt,omitempty"`
	ResponseSchema map[string]any `json:"responseSchema,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type ResumeEntry struct {
	InterruptID string `json:"interruptId"`
	Status      string `json:"status"`
	Payload     any    `json:"payload,omitempty"`
}

type RunAgentInput struct {
	RunID     string           `json:"runId"`
	ThreadID  string           `json:"threadId"`
	State     map[string]any   `json:"state,omitempty"`
	Messages  []ChatMessage    `json:"messages"`
	Tools     []map[string]any `json:"tools,omitempty"`
	Context   map[string]any   `json:"context,omitempty"`
	Forwarded map[string]any   `json:"forwardedProps,omitempty"`
	Resume    []ResumeEntry    `json:"resume,omitempty"`
	Extra     map[string]any   `json:"-"`
}

func NormalizeRole(role string) string {
	trimmed := strings.TrimSpace(role)
	if trimmed == "" {
		return RoleUser
	}
	if _, ok := allowedRoles[trimmed]; ok {
		return trimmed
	}
	if mapped, ok := knownRoleRemaps[trimmed]; ok {
		return mapped
	}
	return RoleUser
}

func NormalizeMessages(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(messages))
	for _, m := range messages {
		m.Role = NormalizeRole(m.Role)
		if m.ID == "" {
			m.ID = mustMsgID()
		}
		out = append(out, m)
	}
	return out
}

func mustMsgID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("msg-%d", time.Now().UnixNano())
	}
	return "msg-" + hex.EncodeToString(buf)
}

func NewToolCall(id, name, arguments string) ToolCall {
	if name == "" {
		name = "tool"
	}
	return ToolCall{
		ID:   id,
		Type: "function",
		Function: ToolFunction{
			Name:      name,
			Arguments: arguments,
		},
	}
}

func NormalizePayloadRoles(value any) any {
	v, ok := value.(map[string]any)
	if !ok {
		return value
	}

	if role, ok := v["role"].(string); ok {
		v["role"] = NormalizeRole(role)
	}

	if msgs, ok := v["messages"].([]any); ok {
		v["messages"] = normalizeMessagesAny(msgs)
	}

	if input, ok := v["input"].(map[string]any); ok {
		if msgs, ok := input["messages"].([]any); ok {
			input["messages"] = normalizeMessagesAny(msgs)
			v["input"] = input
		}
	}

	return v
}

func normalizeMessagesAny(msgs []any) []any {
	out := make([]any, 0, len(msgs))
	for _, msg := range msgs {
		obj, ok := msg.(map[string]any)
		if !ok {
			out = append(out, msg)
			continue
		}
		if role, ok := obj["role"].(string); ok {
			obj["role"] = NormalizeRole(role)
		}
		out = append(out, obj)
	}
	return out
}

func ParseEventEnvelope(rawJSON []byte) (EventEnvelope, error) {
	var payload map[string]any
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		return EventEnvelope{}, err
	}

	payload = NormalizePayloadRoles(payload).(map[string]any)
	typ, _ := payload["type"].(string)
	msgID, _ := payload["messageId"].(string)
	delta, _ := payload["delta"].(string)
	toolCallID, _ := payload["toolCallId"].(string)
	toolCallName, _ := payload["toolCallName"].(string)
	parentMessageID, _ := payload["parentMessageId"].(string)
	content, _ := payload["content"].(string)

	return EventEnvelope{
		Type:            typ,
		Raw:             payload,
		MessageID:       msgID,
		Delta:           delta,
		ToolCallID:      toolCallID,
		ToolCallName:    toolCallName,
		ParentMessageID: parentMessageID,
		Content:         content,
	}, nil
}

func InterruptsFromRunFinished(event EventEnvelope) []Interrupt {
	if event.Type != "RUN_FINISHED" {
		return nil
	}
	outcome, ok := event.Raw["outcome"].(map[string]any)
	if !ok || outcome["type"] != "interrupt" {
		return nil
	}
	rawInterrupts, ok := outcome["interrupts"].([]any)
	if !ok {
		return nil
	}
	interrupts := make([]Interrupt, 0, len(rawInterrupts))
	for _, raw := range rawInterrupts {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := obj["id"].(string)
		reason, _ := obj["reason"].(string)
		if id == "" || reason == "" {
			continue
		}
		interrupt := Interrupt{ID: id, Reason: reason}
		interrupt.Message, _ = obj["message"].(string)
		interrupt.ToolCallID, _ = obj["toolCallId"].(string)
		interrupt.ExpiresAt, _ = obj["expiresAt"].(string)
		if responseSchema, ok := obj["responseSchema"].(map[string]any); ok {
			interrupt.ResponseSchema = responseSchema
		}
		if metadata, ok := obj["metadata"].(map[string]any); ok {
			interrupt.Metadata = metadata
		}
		interrupts = append(interrupts, interrupt)
	}
	return interrupts
}

func MessagesFromSnapshot(raw any) []ChatMessage {
	rawMessages, ok := raw.([]any)
	if !ok {
		return nil
	}
	converted := make([]ChatMessage, 0, len(rawMessages))
	for _, rawMessage := range rawMessages {
		messageObj, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		message, ok := chatMessageFromSnapshot(messageObj)
		if !ok {
			continue
		}
		converted = append(converted, message)
	}
	return converted
}

func chatMessageFromSnapshot(raw map[string]any) (ChatMessage, bool) {
	role, _ := raw["role"].(string)
	role = NormalizeRole(role)
	if role == "" {
		return ChatMessage{}, false
	}
	message := ChatMessage{
		Role:    role,
		Content: raw["content"],
	}
	message.ID, _ = raw["id"].(string)
	message.Name, _ = raw["name"].(string)
	message.ToolCallID = firstString(raw, "toolCallId", "tool_call_id")
	message.Error, _ = raw["error"].(string)
	if message.Content == nil {
		message.Content = ""
	}
	if role == RoleAssistant {
		message.ToolCalls = toolCallsFromSnapshot(raw)
	}
	return message, true
}

func toolCallsFromSnapshot(raw map[string]any) []ToolCall {
	var rawCalls []any
	if calls, ok := raw["toolCalls"].([]any); ok {
		rawCalls = calls
	} else if calls, ok := raw["tool_calls"].([]any); ok {
		rawCalls = calls
	}
	if len(rawCalls) == 0 {
		return nil
	}
	calls := make([]ToolCall, 0, len(rawCalls))
	for _, rawCall := range rawCalls {
		obj, ok := rawCall.(map[string]any)
		if !ok {
			continue
		}
		id := firstString(obj, "toolCallId", "id")
		fn, _ := obj["function"].(map[string]any)
		name := firstString(obj, "toolName", "name")
		args := firstString(obj, "argsText", "arguments")
		if fn != nil {
			if name == "" {
				name, _ = fn["name"].(string)
			}
			if args == "" {
				args, _ = fn["arguments"].(string)
			}
		}
		if id == "" {
			continue
		}
		calls = append(calls, NewToolCall(id, name, args))
	}
	return calls
}

// CancelRunResponse is the response from POST /api/runs/{id}/cancel.
type CancelRunResponse struct {
	Success bool             `json:"success"`
	Run     CancelRunStatus  `json:"run"`
}

// CancelRunStatus is the run status returned in a cancel response.
type CancelRunStatus struct {
	RunID     string `json:"run_id"`
	ThreadID  string `json:"thread_id"`
	Status    string `json:"status"`
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok {
			return value
		}
	}
	return ""
}
