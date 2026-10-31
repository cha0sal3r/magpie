package gateway

import (
	"testing"

	"github.com/cha0sal3r/magpie-friday/internal/provider"
)

// A Chat stream that reports no usage keeps its reply text, so its output
// tokens can be estimated; a stream that reports usage marks it so the
// vendor's count wins.
func TestSniffChatKeepsOutputForEstimate(t *testing.T) {
	s := newSniffer(provider.Chat, "text/event-stream")
	s.write([]byte(sse(
		`data: {"id":"c1","model":"m1","choices":[{"index":0,"delta":{"content":"tiktoken is "}}]}`,
		`data: {"id":"c1","model":"m1","choices":[{"index":0,"delta":{"content":"great!"}}]}`,
		`data: [DONE]`,
	)))
	if s.reported {
		t.Error("no usage went by; reported should be false")
	}
	if got := string(s.outText); got != "tiktoken is great!" {
		t.Errorf("outText %q", got)
	}

	s = newSniffer(provider.Chat, "text/event-stream")
	s.write([]byte(sse(
		`data: {"id":"c1","model":"m1","choices":[{"index":0,"delta":{"content":"x"}}]}`,
		`data: {"id":"c1","model":"m1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3}}`,
		`data: [DONE]`,
	)))
	if !s.reported {
		t.Error("usage went by; reported should be true")
	}
}

// Through the whole gateway: a Chat relay that reports no usage is
// estimated with the tiktoken codec, and a relay that reports its own usage
// is left alone.
func TestChatRelayEstimatesUsage(t *testing.T) {
	reply := `{"id":"c1","model":"m1","choices":[{"index":0,"delta":{"content":"tiktoken is great!"},"finish_reason":"stop"}]}`
	whole := `{"id":"c1","model":"m1","choices":[{"index":0,"message":{"role":"assistant","content":"tiktoken is great!"},"finish_reason":"stop"}]}`
	withUsage := `{"id":"c1","model":"m1","choices":[{"index":0,"message":{"content":"x"}}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`
	for _, c := range []struct {
		name, ctype, reply string
		in, out            int
	}{
		{"stream without usage", "text/event-stream", sse("data: "+reply, "data: [DONE]"), 2, 6},
		{"whole without usage", "application/json", whole, 2, 6},
		{"whole with usage", "application/json", withUsage, 100, 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			setup(t, provider.Chat, &fake{ctype: c.ctype, reply: c.reply})
			code, body := post(t, "/v1/chat/completions", `{"model":"m1","stream":true,"messages":[{"role":"user","content":"hello world"}]}`)
			if code != 200 {
				t.Fatalf("gateway: %d %s", code, body)
			}
			r := lastUsage(t)
			if r.Input != c.in || r.Output != c.out {
				t.Errorf("usage input=%d output=%d, want %d/%d", r.Input, r.Output, c.in, c.out)
			}
		})
	}
}
