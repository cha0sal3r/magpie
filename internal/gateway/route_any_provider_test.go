package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A provider runs its routing strategy from its own file: the model-family
// fallback applies to it too, not just to one special preset. This is the
// "any provider may enable it" guarantee.
func TestAnyProviderRunsARoutingStrategy(t *testing.T) {
	noRests()
	t.Cleanup(noRests)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_, _ = io.WriteString(w, `{"id":"openai-routed","choices":[]}`)
	}))
	t.Cleanup(srv.Close)

	p := provider.Provider{ID: "openai", Name: "OpenAI"}
	p.Chat = srv.URL + "/v1"
	p.Models = []string{"gpt", "gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}

	// its own strategy file: a gpt family it falls back through.
	pol := provider.DefaultRoute()
	pol.Enabled = true
	pol.Family.Aliases = map[string]string{"gpt": "gpt-6-astra"}
	pol.Family.PrefixAliases = nil
	pol.Family.Families = []provider.RouteFamilyList{{Name: "gpt", Models: []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol"}}}
	pol.Family.ToolExcluded = nil
	if err := provider.SaveRoute("openai", pol); err != nil {
		t.Fatal(err)
	}

	code, body := post(t, "/v1/chat/completions", `{"model":"openai/gpt","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || !strings.Contains(body, "openai-routed") {
		t.Fatalf("%d %s", code, body)
	}
	if want := []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol"}; !slices.Equal(seen, want) {
		t.Fatalf("models tried %v, want %v", seen, want)
	}
}
