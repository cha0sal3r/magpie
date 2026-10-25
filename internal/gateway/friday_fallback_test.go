package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func TestFridayFallbackChains(t *testing.T) {
	for _, tc := range []struct {
		model string
		tools bool
		want  []string
	}{
		{"gpt", false, fridayGPT},
		{"gpt", true, fridayGPT[2:]},
		{"claude-opus-4-6", false, []string{"aws.claude-opus-4.8", "aws.claude-opus-4.7", "aws.claude-opus-4.6"}},
		{"aws.claude-opus-4.6", false, []string{"aws.claude-opus-4.6"}},
		{"claude-sonnet-4-6", false, []string{"aws.claude-sonnet-4.6", "aws.claude-sonnet-4.5"}},
		{"claude-haiku-4-5", false, append([]string{"gpt-4o-mini"}, fridayGPT...)},
		{"kimi", false, fridayKimi},
		{"kimi-k2.7-code", false, fridayKimi[2:]},
		{"kimi-unlisted", false, []string{"kimi-unlisted"}},
		{"deepseek-v4-pro", true, []string{"deepseek-v4-pro"}},
	} {
		if got := fridayChain(tc.model, tc.tools); !slices.Equal(got, tc.want) {
			t.Errorf("fridayChain(%q, %v) = %v, want %v", tc.model, tc.tools, got, tc.want)
		}
	}
}

func fridayProvider(t *testing.T, upstream http.Handler) {
	t.Helper()
	restingUntil.Lock()
	restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
	restingUntil.Unlock()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	p := provider.Provider{ID: "friday", Name: "Friday", Preset: "friday"}
	p.Chat = srv.URL + "/v1"
	p.Models = []string{"gpt", "gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.5"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
}

func TestFridayFallsBackWithinModelFamily(t *testing.T) {
	var seen []string
	fridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		if req.Model != "gpt-6-sol" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"friday-fallback","choices":[]}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"friday/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "friday-fallback") {
		t.Fatalf("%d %s", code, body)
	}
	if want := []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol"}; !slices.Equal(seen, want) {
		t.Fatalf("models tried %v, want %v", seen, want)
	}
}

func TestFridayFailureRestsOnlyFailedModel(t *testing.T) {
	restingUntil.Lock()
	restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
	restingUntil.Unlock()
	t.Cleanup(func() {
		restingUntil.Lock()
		restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
		restingUntil.Unlock()
	})
	fridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Model == "gpt-6-astra" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"fallback-ok","choices":[]}`)
	}))
	code, _ := post(t, "/v1/chat/completions", `{"model":"friday/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	restingUntil.Lock()
	_, modelRested := restingUntil.m["friday/gpt-6-astra"]
	_, providerRested := restingUntil.m["friday"]
	restingUntil.Unlock()
	if !modelRested || providerRested {
		t.Fatalf("model rested=%v provider rested=%v", modelRested, providerRested)
	}
}

func TestFridayToolModelExclusionsSurviveAvailableFiltering(t *testing.T) {
	p := provider.Provider{ID: "friday", Name: "Friday", Preset: "friday"}
	p.Chat = "http://127.0.0.1:8080/v1"
	p.Models = []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol"}
	p.NoModels = true
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	saved, err := provider.Find("friday")
	if err != nil {
		t.Fatal(err)
	}
	chain := fridayKnown(*saved, fridayChain("gpt", true))
	if len(chain) == 0 || chain[0] != "gpt-6-sol" || slices.Contains(chain, "gpt-6-astra") || slices.Contains(chain, "gpt-6.1-sol") {
		t.Fatalf("tool-capable available chain = %v", chain)
	}
	if chain := fridayChain("gpt-6-astra", true); len(chain) == 0 || chain[0] != "gpt-6-sol" || slices.Contains(chain, "gpt-6-astra") {
		t.Fatalf("excluded concrete primary got chain %v", chain)
	}
}

func TestFridayToolRequestSkipsBrokenModels(t *testing.T) {
	var seen []string
	fridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"tool-ok","choices":[]}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"friday/gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]}`)
	if code != http.StatusOK || !strings.Contains(body, "tool-ok") || !slices.Equal(seen, []string{"gpt-6-sol"}) {
		t.Fatalf("%d %s, models %v", code, body, seen)
	}
}

func TestFridayDoesNotFallbackOnUnauthorized(t *testing.T) {
	var seen []string
	fridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid Friday token"}}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"friday/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusUnauthorized || !strings.Contains(body, "invalid Friday token") || !slices.Equal(seen, []string{"gpt-6-astra"}) {
		t.Fatalf("%d %s, models %v", code, body, seen)
	}
}

func TestFridayDoesNotFallbackOnBadRequest(t *testing.T) {
	var seen []string
	fridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"messages: field required"}}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"friday/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "field required") || !slices.Equal(seen, []string{"gpt-6-astra"}) {
		t.Fatalf("%d %s, models %v", code, body, seen)
	}
}

// kimiFridayProvider stands a Friday provider serving the Kimi family, for
// the cooldown paths: Kimi models are not in the GPT never-cool set.
func kimiFridayProvider(t *testing.T, upstream http.Handler) {
	t.Helper()
	fridayCools = fridayCooldowns{}
	t.Cleanup(func() { fridayCools = fridayCooldowns{} })
	restingUntil.Lock()
	restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
	restingUntil.Unlock()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	p := provider.Provider{ID: "friday", Name: "Friday", Preset: "friday"}
	p.Chat = srv.URL + "/v1"
	p.Models = []string{"kimi-k3", "kimi-k2.7-code-highspeed", "kimi-k2.7-code"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
}

func TestFridayCooldownOn429(t *testing.T) {
	kimiFridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Model == "kimi-k3" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"ok","choices":[]}`)
	}))
	code, _ := post(t, "/v1/chat/completions", `{"model":"friday/kimi","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !fridayCools.cooled("kimi-k3", time.Now()) {
		t.Error("kimi-k3 should cool after its 429")
	}
	if fridayCools.cooled("kimi-k2.7-code-highspeed", time.Now()) {
		t.Error("the model that answered should not cool")
	}
}

func TestFridaySkipsCoolingModel(t *testing.T) {
	var seen []string
	kimiFridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","choices":[]}`)
	}))
	fridayCools.cool("kimi-k3", false, time.Now())
	code, _ := post(t, "/v1/chat/completions", `{"model":"friday/kimi","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(seen) == 0 || seen[0] != "kimi-k2.7-code-highspeed" {
		t.Fatalf("first model tried %v, want kimi-k2.7-code-highspeed (kimi-k3 cooling)", seen)
	}
}

func TestFridayFallsBackOnErrorChunk(t *testing.T) {
	var seen []string
	fridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		if req.Model == "gpt-6-astra" {
			_, _ = io.WriteString(w, `data: {"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"content":"[Error] rate limit"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			fl.Flush()
			return
		}
		_, _ = io.WriteString(w, `data: {"id":"ok","object":"chat.completion.chunk","created":1,"model":"`+req.Model+`","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"friday/gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if code != http.StatusOK || !strings.Contains(body, "hello") || strings.Contains(body, "[Error]") {
		t.Fatalf("%d %s", code, body)
	}
	if want := []string{"gpt-6-astra", "gpt-6.1-sol"}; !slices.Equal(seen, want) {
		t.Fatalf("models tried %v, want %v", seen, want)
	}
}

func TestFridayFallsBackOnCreatedZero(t *testing.T) {
	var seen []string
	fridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		if req.Model == "gpt-6-astra" {
			_, _ = io.WriteString(w, `data: {"id":"","object":"chat.completion.chunk","created":0,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			fl.Flush()
			return
		}
		_, _ = io.WriteString(w, `data: {"id":"ok","object":"chat.completion.chunk","created":1,"model":"`+req.Model+`","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"friday/gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if code != http.StatusOK || !strings.Contains(body, "hello") {
		t.Fatalf("%d %s", code, body)
	}
	if want := []string{"gpt-6-astra", "gpt-6.1-sol"}; !slices.Equal(seen, want) {
		t.Fatalf("models tried %v, want %v", seen, want)
	}
}

func TestFridayCooldownOnCreatedZero(t *testing.T) {
	kimiFridayProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		if req.Model == "kimi-k3" {
			_, _ = io.WriteString(w, `data: {"id":"","object":"chat.completion.chunk","created":0,"model":"kimi-k3","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			fl.Flush()
			return
		}
		_, _ = io.WriteString(w, `data: {"id":"ok","object":"chat.completion.chunk","created":1,"model":"`+req.Model+`","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	code, _ := post(t, "/v1/chat/completions", `{"model":"friday/kimi","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !fridayCools.cooled("kimi-k3", time.Now()) {
		t.Error("kimi-k3 should cool after its created=0 reply")
	}
	if fridayCools.cooled("kimi-k2.7-code-highspeed", time.Now()) {
		t.Error("the model that answered should not cool")
	}
}
