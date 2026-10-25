package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fridaySSEChunk(delta map[string]any, finish any, id string) []byte {
	body := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": float64(1), "model": "gpt-5.5",
		"choices": []any{map[string]any{"index": float64(0), "delta": delta, "finish_reason": finish}},
	}
	return fridaySSEData(body)
}

func fridaySSEData(v any) []byte {
	b, _ := json.Marshal(v)
	return []byte("data: " + string(b) + "\n\n")
}

func TestClassifyFridayFrame(t *testing.T) {
	t.Run("retry on temporary error", func(t *testing.T) {
		out, cool, app := classifyFridayFrame(fridaySSEData(map[string]any{
			"error": map[string]any{"message": "temporarily unavailable"},
		}))
		if out != "retry" || cool || !app {
			t.Fatalf("got (%q,%v,%v), want (retry,false,true)", out, cool, app)
		}
	})
	t.Run("fatal on invalid request", func(t *testing.T) {
		out, _, _ := classifyFridayFrame(fridaySSEData(map[string]any{
			"error": map[string]any{"type": "invalid_request_error", "message": "bad input"},
		}))
		if out != "fatal" {
			t.Fatalf("outcome = %q, want fatal", out)
		}
	})
	t.Run("retry on created zero without id", func(t *testing.T) {
		frame := fridaySSEChunk(map[string]any{"role": "assistant"}, nil, "")
		var body map[string]any
		_ = json.Unmarshal([]byte(frame[6:]), &body)
		body["created"] = float64(0)
		out, cool, app := classifyFridayFrame(fridaySSEData(body))
		if out != "retry" || cool || !app {
			t.Fatalf("got (%q,%v,%v), want (retry,false,true)", out, cool, app)
		}
	})
	for name, delta := range map[string]map[string]any{
		"content":   {"content": "ok"},
		"reasoning": {"reasoning_content": "thinking"},
		"refusal":   {"refusal": "no"},
		"tool call": {"tool_calls": []any{map[string]any{"index": float64(0)}}},
	} {
		if out, _, _ := classifyFridayFrame(fridaySSEChunk(delta, nil, "chatcmpl-test")); out != "content" {
			t.Errorf("%s: outcome = %q, want content", name, out)
		}
	}
	if out, _, _ := classifyFridayFrame(fridaySSEChunk(map[string]any{}, "stop", "chatcmpl-test")); out != "done" {
		t.Errorf("finish stop: outcome = %q, want done", out)
	}
	if out, cool, app := classifyFridayFrame(fridaySSEChunk(map[string]any{"content": "[Error] rate limit"}, nil, "chatcmpl-test")); out != "retry" || cool || !app {
		t.Errorf("[Error] rate limit: got (%q,%v,%v), want (retry,false,true)", out, cool, app)
	}
}

func TestParseFridayFrame(t *testing.T) {
	if event, body := parseFridayFrame([]byte("data: [DONE]\n\n")); event != "done" || body != nil {
		t.Fatalf("[DONE]: got (%q,%v), want (done,nil)", event, body)
	}
	if event, _ := parseFridayFrame(fridaySSEData(map[string]any{"error": map[string]any{"message": "x"}})); event != "error" {
		t.Fatalf("error frame event = %q, want error", event)
	}
}

func TestFridayCooldownExceptions(t *testing.T) {
	now := time.Now()
	c := &fridayCooldowns{}
	c.cool("aws.claude-opus-4.8", true, now)
	if !c.cooled("aws.claude-opus-4.8", now) {
		t.Error("aws.claude-opus-4.8 should cool on created0")
	}
	c = &fridayCooldowns{}
	c.cool("aws.claude-opus-4.8", false, now)
	if c.cooled("aws.claude-opus-4.8", now) {
		t.Error("aws.claude-opus-4.8 should not cool on a 429")
	}
	c = &fridayCooldowns{}
	c.cool("gpt-6-astra", true, now)
	if c.cooled("gpt-6-astra", now) {
		t.Error("a GPT model should never cool")
	}
	c = &fridayCooldowns{}
	c.cool("kimi-k3", false, now)
	if !c.cooled("kimi-k3", now) {
		t.Error("kimi-k3 should cool on a 429")
	}
}

func TestTruncateFridayMessages(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "policy"},
		map[string]any{"role": "user", "content": "old"},
		map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "1"}}},
		map[string]any{"role": "tool", "tool_call_id": "1", "content": "result"},
		map[string]any{"role": "user", "content": "new"},
	}}
	out := truncateFridayMessages(body, 1)
	msgs, _ := out["messages"].([]any)
	if len(msgs) != 2 || fridayRole(msgs[0]) != "system" || fridayRole(msgs[1]) != "user" {
		t.Fatalf("truncated messages = %v, want [system, user]", msgs)
	}
	if truncateFridayMessages(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "one"}}}, 1) != nil {
		t.Error("a single message should not truncate")
	}
}

func TestFridayPreTruncate(t *testing.T) {
	// 300000 chars ≈ 75000 tokens by the 4 chars/token estimate; three of
	// them ≈ 225000 tokens, over the 180000 pre-truncation bar.
	big := strings.Repeat("x", 300000)
	req := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "sys"},
		map[string]any{"role": "user", "content": big},
		map[string]any{"role": "assistant", "content": big},
		map[string]any{"role": "user", "content": big},
	}}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(fridayPreTruncate(b), &got); err != nil {
		t.Fatal(err)
	}
	msgs, _ := got["messages"].([]any)
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = fridayRole(m)
	}
	if len(msgs) != 2 || roles[0] != "system" || roles[1] != "user" {
		t.Fatalf("pre-truncated messages = %v, want [system user]", roles)
	}
	small := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if out := fridayPreTruncate(small); string(out) != string(small) {
		t.Fatalf("small body changed: %s", out)
	}
}

func TestFridayThrottleConcurrency(t *testing.T) {
	fridaySendMu.Lock()
	fridayLastSend = time.Time{}
	fridaySendMu.Unlock()
	ctx := context.Background()
	r1, err := fridayThrottle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r1)
	r2, err := fridayThrottle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r2)
	// two slots are held; a third throttle must wait for one to free.
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := fridayThrottle(short); err == nil {
		t.Error("a third Friday throttle should block past two")
	}
}
