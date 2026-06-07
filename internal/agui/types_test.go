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
