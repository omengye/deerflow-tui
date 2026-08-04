package agui

import "testing"

func TestNormalizeRole(t *testing.T) {
	cases := map[string]string{
		"ai":        RoleAssistant,
		"human":     RoleUser,
		"function":  RoleTool,
		"assistant": RoleAssistant,
		"unknown":   RoleUser,
		"":          RoleUser,
	}
	for input, want := range cases {
		if got := NormalizeRole(input); got != want {
			t.Fatalf("NormalizeRole(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseEventEnvelopeNormalizesNestedRoles(t *testing.T) {
	env, err := ParseEventEnvelope([]byte(`{"type":"CUSTOM","role":"ai","messages":[{"role":"human","content":"hi"}],"input":{"messages":[{"role":"function","content":"ok"}]}}`))
	if err != nil {
		t.Fatalf("ParseEventEnvelope returned error: %v", err)
	}
	if env.Raw["role"] != RoleAssistant {
		t.Fatalf("top-level role = %#v", env.Raw["role"])
	}
	messages := env.Raw["messages"].([]any)
	if messages[0].(map[string]any)["role"] != RoleUser {
		t.Fatalf("message role = %#v", messages[0])
	}
	input := env.Raw["input"].(map[string]any)
	inputMessages := input["messages"].([]any)
	if inputMessages[0].(map[string]any)["role"] != RoleTool {
		t.Fatalf("input message role = %#v", inputMessages[0])
	}
}

func TestMessagesFromSnapshotPreservesToolResults(t *testing.T) {
	messages := MessagesFromSnapshot([]any{
		map[string]any{"id": "a1", "role": "assistant", "content": "", "toolCalls": []any{
			map[string]any{"id": "tc1", "function": map[string]any{"name": "lookup", "arguments": `{"q":"x"}`}},
		}},
		map[string]any{"id": "t1", "role": "tool", "toolCallId": "tc1", "content": `{"ok":true}`},
	})

	if len(messages) != 2 {
		t.Fatalf("expected assistant and tool messages, got %#v", messages)
	}
	if messages[1].Role != RoleTool || messages[1].ToolCallID != "tc1" || messages[1].Content != `{"ok":true}` {
		t.Fatalf("tool result was not preserved: %#v", messages[1])
	}
}

func TestMessagesFromSnapshotAcceptsReasoningTypeAndText(t *testing.T) {
	messages := MessagesFromSnapshot([]any{
		map[string]any{"id": "r1", "type": "reasoning", "text": "saved plan"},
	})

	if len(messages) != 1 {
		t.Fatalf("expected one reasoning message, got %#v", messages)
	}
	if messages[0].Role != RoleReasoning || messages[0].Content != "saved plan" || messages[0].ID != "r1" {
		t.Fatalf("reasoning message was not preserved: %#v", messages[0])
	}
}

func TestMessagesFromSnapshotSkipsEntriesWithoutRoleOrType(t *testing.T) {
	messages := MessagesFromSnapshot([]any{
		map[string]any{"id": "unknown", "content": "must not become a user message"},
	})
	if len(messages) != 0 {
		t.Fatalf("snapshot entry without role/type was incorrectly imported: %#v", messages)
	}
}
