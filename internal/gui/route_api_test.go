package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cha0sal3r/magpie-friday/internal/provider"
)

// routeAPI stands up the provider routes with a scratch config dir and
// returns a request helper.
func routeAPI(t *testing.T) func(method, path, body string) (int, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	return func(method, path, body string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		mux.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
}

// The routing strategy's API reads a provider's file (or an off template for
// everyone with none), and writes it back.
func TestRouteAPI(t *testing.T) {
	req := routeAPI(t)
	type out struct {
		Policy  provider.RoutePolicy `json:"policy"`
		HasFile bool                 `json:"hasFile"`
	}
	get := func(id string) out {
		t.Helper()
		code, body := req("GET", "/api/providers/"+id+"/route", "")
		if code != 200 {
			t.Fatalf("GET %d %s", code, body)
		}
		var o out
		if err := json.Unmarshal([]byte(body), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}

	// a preset provider with no file is off like any other provider.
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Preset: "relay", Chat: "http://127.0.0.1:1/v1", Models: []string{"gpt"}}); err != nil {
		t.Fatal(err)
	}
	if o := get("relay"); o.Policy.Enabled || o.HasFile || o.Policy.Cooldown.Seconds != 60 {
		t.Fatalf("relay default: enabled=%v hasFile=%v cooldown=%d", o.Policy.Enabled, o.HasFile, o.Policy.Cooldown.Seconds)
	}

	// another provider with no file: off, no file.
	if err := provider.Save(provider.Provider{ID: "openai", Name: "OpenAI", Chat: "http://127.0.0.1:1/v1", Models: []string{"gpt-5"}}); err != nil {
		t.Fatal(err)
	}
	if o := get("openai"); o.Policy.Enabled || o.HasFile {
		t.Fatalf("openai default: enabled=%v hasFile=%v", o.Policy.Enabled, o.HasFile)
	}

	// save a strategy for openai: it comes back as a file, normalized.
	pol := provider.DefaultRoute()
	pol.Throttle.MaxConcurrent = 7
	b, _ := json.Marshal(pol)
	if code, body := req("POST", "/api/providers/openai/route", string(b)); code != 200 {
		t.Fatalf("POST %d %s", code, body)
	}
	if o := get("openai"); !o.Policy.Enabled || !o.HasFile || o.Policy.Throttle.MaxConcurrent != 7 {
		t.Fatalf("saved: enabled=%v hasFile=%v maxConcurrent=%d", o.Policy.Enabled, o.HasFile, o.Policy.Throttle.MaxConcurrent)
	}

	// switch it off with nothing else set: the file goes away.
	if code, body := req("POST", "/api/providers/openai/route", `{"enabled":false}`); code != 200 {
		t.Fatalf("POST off %d %s", code, body)
	}
	if o := get("openai"); o.Policy.Enabled || o.HasFile {
		t.Fatalf("off: enabled=%v hasFile=%v", o.Policy.Enabled, o.HasFile)
	}
	if _, ok := provider.LoadRoute("openai"); ok {
		t.Fatal("off strategy should leave no file")
	}
}

// Saving with ?generate=1 fills the family list from the provider's models.
func TestRouteAPIGenerate(t *testing.T) {
	req := routeAPI(t)
	if err := provider.Save(provider.Provider{ID: "genroute", Name: "Gen", Chat: "http://127.0.0.1:1/v1", Models: []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-1.5-pro"}}); err != nil {
		t.Fatal(err)
	}
	code, body := req("POST", "/api/providers/genroute/route?generate=1", `{"enabled":true}`)
	if code != 200 {
		t.Fatalf("POST generate %d %s", code, body)
	}
	var o struct {
		Policy  provider.RoutePolicy `json:"policy"`
		HasFile bool                 `json:"hasFile"`
	}
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatal(err)
	}
	// every Gemini model shares the "gemini" stem: one listless family.
	if len(o.Policy.Family.Families) != 1 || o.Policy.Family.Families[0].Name != "gemini" {
		t.Fatalf("generated families: %+v", o.Policy.Family.Families)
	}
}
