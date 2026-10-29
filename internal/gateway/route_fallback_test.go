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

// familyModels is the models of one family in a policy.
func familyModels(pol provider.RoutePolicy, name string) []string {
	for _, f := range pol.Family.Families {
		if f.Name == name {
			return f.Models
		}
	}
	return nil
}

// testRoute is the model-family plan the fallback and cooldown tests use as
// their fixture.
func testRoute() provider.RoutePolicy {
	return provider.RoutePolicy{
		Enabled: true,
		Family: provider.RouteFamily{
			Aliases: map[string]string{
				"gpt":               "gpt-6-astra",
				"kimi":              "kimi-k3",
				"deepseek":          "deepseek-v4-pro",
				"claude-opus-4-8":   "aws.claude-opus-4.8",
				"claude-opus-4-7":   "aws.claude-opus-4.8",
				"claude-opus-4-6":   "aws.claude-opus-4.8",
				"claude-sonnet-4-6": "aws.claude-sonnet-4.6",
				"claude-sonnet-4-5": "aws.claude-sonnet-4.5",
				"claude-haiku-4-5":  "gpt-4o-mini",
			},
			PrefixAliases: map[string]string{"claude-haiku": "gpt-4o-mini"},
			Families: []provider.RouteFamilyList{
				{Name: "opus", Models: []string{"claude-opus-5", "aws.claude-opus-4.8", "aws.claude-opus-4.7", "aws.claude-opus-4.6"}},
				{Name: "sonnet", Models: []string{"claude-sonnet-5", "aws.claude-sonnet-4.6", "aws.claude-sonnet-4.5"}},
				{Name: "gpt", Models: []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.5"}},
				{Name: "kimi", Models: []string{"kimi-k3", "kimi-k2.8-preview", "kimi-k2.7-code-highspeed", "kimi-k2.7-code", "kimi-k2.6"}, Exclude: []string{"kimi-unlisted"}},
				{Name: "deepseek"},
			},
			ToolExcluded: []string{"gpt-6-astra", "gpt-6.1-sol"},
		},
		Cooldown: provider.RouteCooldown{
			Seconds:   60,
			No429:     []string{"aws.claude-opus-4.8"},
			NeverCool: []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.5"},
		},
	}
}

// routeTestRuntime is the "relay" test provider's route runtime.
func routeTestRuntime() *routeRuntime {
	return runtimeOf("relay", provider.DefaultRoute().Throttle.MaxConcurrent)
}

func TestRouteFallbackChains(t *testing.T) {
	pol := testRoute()
	gpt := familyModels(pol, "gpt")
	kimi := familyModels(pol, "kimi")
	sonnet := familyModels(pol, "sonnet")
	// the provider's model list, as its /models endpoint reports it; the
	// deepseek family names none, so it is derived from these, and it is the
	// last family, so after a model's own family the chain goes on to the
	// families below it (never up).
	avail := []string{
		"claude-opus-5", "aws.claude-opus-4.8", "aws.claude-opus-4.7", "aws.claude-opus-4.6",
		"claude-sonnet-5", "aws.claude-sonnet-4.6", "aws.claude-sonnet-4.5",
		"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.5",
		"kimi-k3", "kimi-k2.8-preview", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6",
		"deepseek-v4-pro", "deepseek-v4-pro-baidu", "deepseek-v4-pro-tencent",
	}
	ds := []string{"deepseek-v4-pro", "deepseek-v4-pro-baidu", "deepseek-v4-pro-tencent"}
	cat := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	for _, tc := range []struct {
		model string
		tools bool
		want  []string
	}{
		{"gpt", false, cat(gpt, kimi, ds)},
		{"gpt", true, cat(gpt[2:], kimi, ds)},
		{"claude-opus-4-6", false, cat([]string{"aws.claude-opus-4.8", "aws.claude-opus-4.7", "aws.claude-opus-4.6"}, sonnet, gpt, kimi, ds)},
		{"aws.claude-opus-4.6", false, cat([]string{"aws.claude-opus-4.6"}, sonnet, gpt, kimi, ds)},
		{"claude-sonnet-4-6", false, cat([]string{"aws.claude-sonnet-4.6", "aws.claude-sonnet-4.5"}, gpt, kimi, ds)},
		{"claude-haiku-4-5", false, cat([]string{"gpt-4o-mini"}, gpt, kimi, ds)},
		{"kimi", false, cat(kimi, ds)},
		{"kimi-k2.7-code", false, cat(kimi[3:], ds)},
		{"kimi-unlisted", false, []string{"kimi-unlisted"}},
		{"deepseek-v4-pro", true, []string{"deepseek-v4-pro", "deepseek-v4-pro-baidu", "deepseek-v4-pro-tencent"}},
	} {
		if got := routeChain(pol, avail, tc.model, tc.tools); !slices.Equal(got, tc.want) {
			t.Errorf("routeChain(%q, %v) = %v, want %v", tc.model, tc.tools, got, tc.want)
		}
	}
}

// A family that names no models is derived from the channel's own list,
// newest version first; a mirror the channel adds (shenzhen) falls back
// without an edit to the file, and an older model is a last resort.
func TestRouteFamilyDerivedFromAvailable(t *testing.T) {
	pol := testRoute()
	avail := []string{
		"deepseek", "deepseek-v3-relay", "deepseek-v4-flash", "deepseek-v4-pro",
		"deepseek-v4-pro-baidu", "deepseek-v4-pro-shenzhen", "deepseek-v4-pro-tencent",
	}
	got := routeChain(pol, avail, "deepseek-v4-pro", false)
	want := []string{"deepseek-v4-pro", "deepseek-v4-pro-baidu", "deepseek-v4-pro-shenzhen", "deepseek-v4-pro-tencent", "deepseek-v3-relay", "deepseek"}
	if !slices.Equal(got, want) {
		t.Fatalf("routeChain = %v, want %v", got, want)
	}
}

// A family that lists its priority appends a model the channel has that the
// file did not name, newest version first, as a last-resort fallback, then
// the families below it (the kimi family lists its own models even when the
// channel serves none of them; the deepseek family derives nothing here).
func TestRouteFamilyAppendsNewChannelModel(t *testing.T) {
	pol := testRoute()
	avail := []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-5.5", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-6.2-sol"}
	got := routeChain(pol, avail, "gpt-6-astra", false)
	want := append(slices.Clone([]string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.5", "gpt-6.2-sol"}), familyModels(pol, "kimi")...)
	if !slices.Equal(got, want) {
		t.Fatalf("routeChain = %v, want %v", got, want)
	}
}

// A series with a single member still has a fallback: once its own family is
// spent, the chain goes on to the families listed below it, and never climbs
// to a newer one.
func TestRouteChainFallsThroughFamilies(t *testing.T) {
	pol := provider.RoutePolicy{
		Family: provider.RouteFamily{
			Families: []provider.RouteFamilyList{
				{Name: "opus", Models: []string{"claude-opus-5"}},
				{Name: "sonnet", Models: []string{"claude-sonnet-4-6", "claude-sonnet-4-5"}},
			},
		},
	}
	avail := []string{"claude-opus-5", "claude-sonnet-4-6", "claude-sonnet-4-5"}
	if got := routeChain(pol, avail, "claude-opus-5", false); !slices.Equal(got, []string{"claude-opus-5", "claude-sonnet-4-6", "claude-sonnet-4-5"}) {
		t.Fatalf("singleton series chain = %v, want the sonnet family below it", got)
	}
	if got := routeChain(pol, avail, "claude-sonnet-4-6", false); !slices.Equal(got, []string{"claude-sonnet-4-6", "claude-sonnet-4-5"}) {
		t.Fatalf("middle series chain = %v, want no climb to opus", got)
	}
	if got := routeChain(pol, avail, "claude-sonnet-4-5", false); !slices.Equal(got, []string{"claude-sonnet-4-5"}) {
		t.Fatalf("last series chain = %v, want alone", got)
	}
}

func routeProvider(t *testing.T, upstream http.Handler) {
	t.Helper()
	restingUntil.Lock()
	restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
	restingUntil.Unlock()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	p := provider.Provider{ID: "relay", Name: "Relay", Preset: "relay"}
	p.Chat = srv.URL + "/v1"
	p.Models = []string{"gpt", "gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.5"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveRoute("relay", testRoute()); err != nil {
		t.Fatal(err)
	}
}

func TestRouteFallsBackWithinModelFamily(t *testing.T) {
	var seen []string
	routeProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_, _ = io.WriteString(w, `{"id":"relay-fallback","choices":[]}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"relay/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "relay-fallback") {
		t.Fatalf("%d %s", code, body)
	}
	if want := []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol"}; !slices.Equal(seen, want) {
		t.Fatalf("models tried %v, want %v", seen, want)
	}
}

// A request whose whole series is spent goes on to the family below it (a
// singleton series still has a fallback), and one in a newer family is never
// asked for a request of an older series.
func TestRouteFallsBackToTheNextFamily(t *testing.T) {
	noRests()
	t.Cleanup(noRests)
	routeRuntimes.Delete("relay")
	t.Cleanup(func() { routeRuntimes.Delete("relay") })
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		if req.Model != "claude-sonnet-4-5" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"next-family-ok","choices":[]}`)
	}))
	t.Cleanup(srv.Close)
	p := provider.Provider{ID: "relay", Name: "Relay", Preset: "relay"}
	p.Chat = srv.URL + "/v1"
	p.Models = []string{"claude-opus-5", "claude-sonnet-4-6", "claude-sonnet-4-5"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveRoute("relay", provider.RoutePolicy{
		Enabled: true,
		Family: provider.RouteFamily{
			Families: []provider.RouteFamilyList{
				{Name: "opus", Models: []string{"claude-opus-5"}},
				{Name: "sonnet", Models: []string{"claude-sonnet-4-6", "claude-sonnet-4-5"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// the singleton opus series is refused whole, so the request falls
	// through the sonnet family below it and answers there.
	seen = nil
	code, body := post(t, "/v1/chat/completions", `{"model":"relay/claude-opus-5","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "next-family-ok") {
		t.Fatalf("opus request: %d %s", code, body)
	}
	if want := []string{"claude-opus-5", "claude-sonnet-4-6", "claude-sonnet-4-5"}; !slices.Equal(seen, want) {
		t.Fatalf("opus request tried %v, want %v", seen, want)
	}
	// a newer series is never climbed to: without the first request's rests
	// and cooldowns, the sonnet family answers within itself, opus not asked
	// (a resting primary would be tried last either way, so the rests are
	// cleared to show the chain alone).
	noRests()
	routeRuntimes.Delete("relay")
	seen = nil
	code, body = post(t, "/v1/chat/completions", `{"model":"relay/claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "next-family-ok") {
		t.Fatalf("sonnet request: %d %s", code, body)
	}
	if want := []string{"claude-sonnet-4-6", "claude-sonnet-4-5"}; !slices.Equal(seen, want) {
		t.Fatalf("sonnet request tried %v, want %v (opus must not be asked)", seen, want)
	}
}

func TestRouteFailureRestsOnlyFailedModel(t *testing.T) {
	restingUntil.Lock()
	restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
	restingUntil.Unlock()
	t.Cleanup(func() {
		restingUntil.Lock()
		restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
		restingUntil.Unlock()
	})
	routeProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	code, _ := post(t, "/v1/chat/completions", `{"model":"relay/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	restingUntil.Lock()
	_, modelRested := restingUntil.m["relay/gpt-6-astra"]
	_, providerRested := restingUntil.m["relay"]
	restingUntil.Unlock()
	if !modelRested || providerRested {
		t.Fatalf("model rested=%v provider rested=%v", modelRested, providerRested)
	}
}

func TestRouteToolModelExclusionsSurviveAvailableFiltering(t *testing.T) {
	pol := testRoute()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := provider.Provider{ID: "relay", Name: "Relay", Preset: "relay"}
	p.Chat = "http://127.0.0.1:8080/v1"
	p.Models = []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol"}
	p.NoModels = true
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	saved, err := provider.Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	avail := routeAvailableIDs(*saved)
	chain := routeKnown(avail, routeChain(pol, avail, "gpt", true))
	if len(chain) == 0 || chain[0] != "gpt-6-sol" || slices.Contains(chain, "gpt-6-astra") || slices.Contains(chain, "gpt-6.1-sol") {
		t.Fatalf("tool-capable available chain = %v", chain)
	}
	if chain := routeChain(pol, avail, "gpt-6-astra", true); len(chain) == 0 || chain[0] != "gpt-6-sol" || slices.Contains(chain, "gpt-6-astra") {
		t.Fatalf("excluded concrete primary got chain %v", chain)
	}
}

func TestRouteToolRequestSkipsBrokenModels(t *testing.T) {
	var seen []string
	routeProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"tool-ok","choices":[]}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"relay/gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]}`)
	if code != http.StatusOK || !strings.Contains(body, "tool-ok") || !slices.Equal(seen, []string{"gpt-6-sol"}) {
		t.Fatalf("%d %s, models %v", code, body, seen)
	}
}

func TestRouteDoesNotFallbackOnUnauthorized(t *testing.T) {
	var seen []string
	routeProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid relay token"}}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"relay/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusUnauthorized || !strings.Contains(body, "invalid relay token") || !slices.Equal(seen, []string{"gpt-6-astra"}) {
		t.Fatalf("%d %s, models %v", code, body, seen)
	}
}

func TestRouteDoesNotFallbackOnBadRequest(t *testing.T) {
	var seen []string
	routeProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"messages: field required"}}`)
	}))
	code, body := post(t, "/v1/chat/completions", `{"model":"relay/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "field required") || !slices.Equal(seen, []string{"gpt-6-astra"}) {
		t.Fatalf("%d %s, models %v", code, body, seen)
	}
}

// kimiRouteProvider stands a test provider serving the Kimi family, for
// the cooldown paths: Kimi models are not in the GPT never-cool set.
func kimiRouteProvider(t *testing.T, upstream http.Handler) {
	t.Helper()
	routeRuntimes.Delete("relay")
	t.Cleanup(func() { routeRuntimes.Delete("relay") })
	restingUntil.Lock()
	restingUntil.m, restingUntil.note = map[string]time.Time{}, map[string]Rest{}
	restingUntil.Unlock()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	p := provider.Provider{ID: "relay", Name: "Relay", Preset: "relay"}
	p.Chat = srv.URL + "/v1"
	p.Models = []string{"kimi-k3", "kimi-k2.7-code-highspeed", "kimi-k2.7-code"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveRoute("relay", testRoute()); err != nil {
		t.Fatal(err)
	}
}

func TestRouteCooldownOn429(t *testing.T) {
	kimiRouteProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	code, _ := post(t, "/v1/chat/completions", `{"model":"relay/kimi","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !routeTestRuntime().cooled("kimi-k3", time.Now()) {
		t.Error("kimi-k3 should cool after its 429")
	}
	if routeTestRuntime().cooled("kimi-k2.7-code-highspeed", time.Now()) {
		t.Error("the model that answered should not cool")
	}
}

func TestRouteSkipsCoolingModel(t *testing.T) {
	var seen []string
	kimiRouteProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","choices":[]}`)
	}))
	routeTestRuntime().cool(testRoute(), "kimi-k3", false, time.Now())
	code, _ := post(t, "/v1/chat/completions", `{"model":"relay/kimi","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(seen) == 0 || seen[0] != "kimi-k2.7-code-highspeed" {
		t.Fatalf("first model tried %v, want kimi-k2.7-code-highspeed (kimi-k3 cooling)", seen)
	}
}

func TestRouteFallsBackOnErrorChunk(t *testing.T) {
	var seen []string
	routeProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	code, body := post(t, "/v1/chat/completions", `{"model":"relay/gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if code != http.StatusOK || !strings.Contains(body, "hello") || strings.Contains(body, "[Error]") {
		t.Fatalf("%d %s", code, body)
	}
	if want := []string{"gpt-6-astra", "gpt-6.1-sol"}; !slices.Equal(seen, want) {
		t.Fatalf("models tried %v, want %v", seen, want)
	}
}

func TestRouteFallsBackOnCreatedZero(t *testing.T) {
	var seen []string
	routeProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	code, body := post(t, "/v1/chat/completions", `{"model":"relay/gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if code != http.StatusOK || !strings.Contains(body, "hello") {
		t.Fatalf("%d %s", code, body)
	}
	if want := []string{"gpt-6-astra", "gpt-6.1-sol"}; !slices.Equal(seen, want) {
		t.Fatalf("models tried %v, want %v", seen, want)
	}
}

func TestRouteCooldownOnCreatedZero(t *testing.T) {
	kimiRouteProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	code, _ := post(t, "/v1/chat/completions", `{"model":"relay/kimi","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !routeTestRuntime().cooled("kimi-k3", time.Now()) {
		t.Error("kimi-k3 should cool after its created=0 reply")
	}
	if routeTestRuntime().cooled("kimi-k2.7-code-highspeed", time.Now()) {
		t.Error("the model that answered should not cool")
	}
}
