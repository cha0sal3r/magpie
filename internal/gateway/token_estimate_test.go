package gateway

import "testing"

// The counts below are the canonical tiktoken values from OpenAI's tiktoken
// README: cl100k_base reads "hello world" as two tokens and "tiktoken is
// great!" as six.

func TestCountTokensCl100k(t *testing.T) {
	for _, c := range []struct {
		text, model string
		want        int
	}{
		{"hello world", "gpt-4", 2},
		{"tiktoken is great!", "gpt-4", 6},
		{"", "gpt-4", 0},
		{"hello world", "deepseek-chat", 2},
	} {
		if got := countTokens(c.text, c.model); got != c.want {
			t.Errorf("countTokens(%q, %q) = %d, want %d", c.text, c.model, got, c.want)
		}
	}
}

// isO200k picks o200k_base for the families new-api does, and cl100k_base for
// everything else.
func TestIsO200k(t *testing.T) {
	for model, want := range map[string]bool{
		"gpt-4o":         true,
		"gpt-4o-mini":    true,
		"gpt-4.1":        true,
		"o1":             true,
		"o3-mini":        true,
		"o4-mini":        true,
		"gpt-5":          true,
		"chatgpt-4o":     true,
		"gpt-4":          false,
		"gpt-3.5-turbo":  false,
		"claude-opus-5":  false,
		"deepseek-chat":  false,
		"doubao-1.5-pro": false,
	} {
		if got := isO200k(model); got != want {
			t.Errorf("isO200k(%q) = %v, want %v", model, got, want)
		}
	}
}

// estimateTokens counts the same fields estimate counts, but with a codec:
// the system prompt, every message part, and every tool.
func TestEstimateTokens(t *testing.T) {
	req := &Request{
		System:   "You are a helpful assistant.",
		Messages: []Message{{Parts: []Part{{Text: "hello world", Name: "get_weather", Args: []byte(`{"city":"Paris"}`)}}}},
		Tools:    []Tool{{Name: "get_weather", Description: "read the weather", Schema: []byte(`{"type":"object"}`)}},
	}
	want := countTokens(req.System, "gpt-4") +
		countTokens("hello world", "gpt-4") +
		countTokens("get_weather", "gpt-4") +
		countTokens(`{"city":"Paris"}`, "gpt-4") +
		countTokens("get_weather", "gpt-4") +
		countTokens("read the weather", "gpt-4") +
		countTokens(`{"type":"object"}`, "gpt-4")
	if got := estimateTokens(req, "gpt-4"); got != want {
		t.Errorf("estimateTokens = %d, want %d", got, want)
	}
	if got := estimateTokens(req, "gpt-4o"); got != countTokens(req.System, "gpt-4o")+countTokens("hello world", "gpt-4o")+countTokens("get_weather", "gpt-4o")+countTokens(`{"city":"Paris"}`, "gpt-4o")+countTokens("get_weather", "gpt-4o")+countTokens("read the weather", "gpt-4o")+countTokens(`{"type":"object"}`, "gpt-4o") {
		t.Errorf("estimateTokens (o200k) = %d", got)
	}
}
