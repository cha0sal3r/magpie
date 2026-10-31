package gui

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cha0sal3r/magpie-friday/internal/catalog"
	"github.com/cha0sal3r/magpie-friday/internal/usage"
)

func nearCost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// The Usage page's planner prices the period's tokens at each first-party
// model's base list price, cheapest first. A token kind a model doesn't bill
// is priced as input (a model without a cache price doesn't cache, so its
// reads are misses), and a Claude model's 1-hour write is derived at 2× input.
func TestUsagePlanner(t *testing.T) {
	sandboxHome(t)
	if err := os.MkdirAll(filepath.Dir(catalog.CachePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog.CachePath(), []byte(`{
  "openai":{"id":"openai","name":"OpenAI","models":{
    "gpt-x":{"id":"gpt-x","name":"GPT X","cost":{"input":2,"output":10,"cache_read":0.1,"cache_write":2.5}}}},
  "anthropic":{"id":"anthropic","name":"Anthropic","models":{
    "claude-x":{"id":"claude-x","name":"Claude X","cost":{"input":3,"output":15,"cache_read":0.15,"cache_write":3.75}}}},
  "deepseek":{"id":"deepseek","name":"DeepSeek","models":{
    "deepseek-x":{"id":"deepseek-x","name":"DeepSeek X","cost":{"input":0.15,"output":0.6,"cache_read":0.003}}}},
  "moonshotai":{"id":"moonshotai","name":"Moonshot","models":{
    "kimi-x":{"id":"kimi-x","name":"Kimi X","cost":{"input":1,"output":4,"cache_read":0.2,"cache_write":1}}}},
  "zhipuai":{"id":"zhipuai","name":"GLM","models":{
    "glm-x":{"id":"glm-x","name":"GLM X","cost":{"input":0.6,"output":2.2,"cache_read":0.11}}}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog.Reset()
	t.Cleanup(catalog.Reset)

	now := holdUsageClock(t, time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local))
	at := usage.Today.Since(now).Add(time.Second)
	usage.Append(usage.Record{Time: at, Agent: "codex", Provider: "openai", Model: "gpt-x",
		Input: 1000, Output: 100, CacheRead: 9000, CacheWrite: 500, CacheWrite1h: 100, Status: 200})

	mux := http.NewServeMux()
	usageRoutes(mux, folderOnly{})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/usage/planner?period=7d", nil))
	var p plannerJSON
	if err := json.Unmarshal(w.Body.Bytes(), &p); w.Code != 200 || err != nil {
		t.Fatalf("%d %s (%v)", w.Code, w.Body, err)
	}
	if p.Calls != 1 || p.Input != 1000 || p.Output != 100 || p.Cache != 9000 {
		t.Fatalf("totals = %+v", p)
	}
	if len(p.Models) != 5 {
		t.Fatalf("models = %d, want 5", len(p.Models))
	}
	for i := 1; i < len(p.Models); i++ {
		if p.Models[i-1].Cost > p.Models[i].Cost {
			t.Fatalf("not cheapest first: %s %.6f before %s %.6f",
				p.Models[i-1].ID, p.Models[i-1].Cost, p.Models[i].ID, p.Models[i].Cost)
		}
	}
	find := func(id string) plannerModel {
		t.Helper()
		for _, m := range p.Models {
			if m.ID == id {
				return m
			}
		}
		t.Fatalf("no %s", id)
		return plannerModel{}
	}
	// DeepSeek lists no cache-write price, so its writes are billed as input.
	if ds := find("deepseek-x"); !nearCost(ds.Cost, (1000*0.15+100*0.6+9000*0.003+500*0.15)/1e6) {
		t.Errorf("deepseek-x = %.6f, want %.6f", ds.Cost, (1000*0.15+100*0.6+9000*0.003+500*0.15)/1e6)
	}
	// OpenAI's 5-minute write covers every cache write, its own 1-hour none.
	if gpt := find("gpt-x"); !nearCost(gpt.Cost, (1000*2+100*10+9000*0.1+500*2.5)/1e6) {
		t.Errorf("gpt-x = %.6f, want %.6f", gpt.Cost, (1000*2+100*10+9000*0.1+500*2.5)/1e6)
	}
	// Claude's 1-hour write is derived at 2× input, so its 100 1-hour writes
	// bill at 6, the other 400 at the 5-minute 3.75.
	if cl := find("claude-x"); !nearCost(cl.Cost, (1000*3+100*15+9000*0.15+400*3.75+100*6)/1e6) {
		t.Errorf("claude-x = %.6f, want %.6f", cl.Cost, (1000*3+100*15+9000*0.15+400*3.75+100*6)/1e6)
	}
}
