package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderModelCooldownBackoffIsolationAndSuccess(t *testing.T) {
	store := providerModelCooldowns{states: map[providerModelKey]providerModelState{}}
	now := time.Unix(1_700_000_000, 0)
	key := providerModelKey{provider: "a", model: "model-a"}
	for i, want := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second, 60 * time.Second} {
		until, failures := store.fail(key, 0, now)
		if got := until.Sub(now); got != want || failures != min(i+1, 4) {
			t.Fatalf("429 #%d: cooldown %s failures %d, want %s/%d", i+1, got, failures, want, min(i+1, 4))
		}
	}
	if _, blocked := store.blocked(providerModelKey{provider: "a", model: "model-b"}, now); blocked {
		t.Fatal("another model was cooled")
	}
	if _, blocked := store.blocked(providerModelKey{provider: "b", model: "model-a"}, now); blocked {
		t.Fatal("another provider was cooled")
	}
	store.success(key, 0)
	if _, blocked := store.blocked(key, now); blocked {
		t.Fatal("success did not clear cooldown")
	}
	until, failures := store.fail(key, 0, now)
	if until.Sub(now) != providerModelCooldownBase || failures != 1 {
		t.Fatalf("after success: %s/%d", until.Sub(now), failures)
	}
	later := now.Add(providerModelCooldownRetention + time.Second)
	until, failures = store.fail(key, 0, later)
	if until.Sub(later) != providerModelCooldownBase || failures != 1 {
		t.Fatalf("after retention: %s/%d", until.Sub(later), failures)
	}
}

func TestProviderModelCooldownSingleRecoveryProbe(t *testing.T) {
	store := providerModelCooldowns{states: map[providerModelKey]providerModelState{}}
	now := time.Unix(1_700_000_000, 0)
	key := providerModelKey{provider: "a", model: "m"}
	store.fail(key, 0, now)
	c := candidate{p: provider.Provider{ID: "a"}, model: "m", rest: "a"}
	if got, leases, _, cooled := store.selectForRoute([]candidate{c}, now.Add(time.Second)); len(got) != 0 || len(leases) != 0 || !cooled {
		t.Fatalf("during cooldown: got %d leases %d cooled %v", len(got), len(leases), cooled)
	}
	probeAt := now.Add(providerModelCooldownBase)
	got, leases, _, _ := store.selectForRoute([]candidate{c}, probeAt)
	token := leases[key]
	if len(got) != 1 || token == 0 {
		t.Fatalf("first probe: got %d token %d", len(got), token)
	}
	if got, _, _, _ := store.selectForRoute([]candidate{c}, probeAt); len(got) != 0 {
		t.Fatal("concurrent probe was admitted")
	}
	// A released probe is reacquired with a fresh token; a stale token
	// releases nothing.
	store.release(key, token, probeAt)
	got, leases, _, _ = store.selectForRoute([]candidate{c}, probeAt)
	newToken := leases[key]
	if len(got) != 1 || newToken == 0 || newToken == token {
		t.Fatal("released probe was not reacquired with a fresh token")
	}
	store.release(key, token, probeAt)
	if got, _, _, _ := store.selectForRoute([]candidate{c}, probeAt); len(got) != 0 {
		t.Fatal("stale token released a newer probe")
	}
	// A probe whose lease expires admits a fresh one.
	if got, leases, _, _ := store.selectForRoute([]candidate{c}, probeAt.Add(providerModelProbeLease)); len(got) != 1 || leases[key] == 0 || leases[key] == newToken {
		t.Fatal("an expired probe lease did not permit a new probe")
	}
}

func TestProviderModelCooldownProbe429AndSuccess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key := providerModelKey{provider: "a", model: "m"}
	c := candidate{p: provider.Provider{ID: "a"}, model: "m", rest: "a"}

	// A recovery probe that 429s again backs the pair off further.
	store := providerModelCooldowns{states: map[providerModelKey]providerModelState{}}
	store.fail(key, 0, now)
	_, leases, _, _ := store.selectForRoute([]candidate{c}, now.Add(providerModelCooldownBase))
	token := leases[key]
	until, failures := store.fail(key, token, now.Add(providerModelCooldownBase))
	if failures != 2 || until.Sub(now.Add(providerModelCooldownBase)) != 2*providerModelCooldownBase {
		t.Fatalf("probe 429: %s/%d", until.Sub(now.Add(providerModelCooldownBase)), failures)
	}
	if _, blocked := store.blocked(key, now.Add(providerModelCooldownBase)); !blocked {
		t.Fatal("a 429 probe did not cool the pair")
	}

	// A recovery probe that answers clears the pair entirely.
	store = providerModelCooldowns{states: map[providerModelKey]providerModelState{}}
	store.fail(key, 0, now)
	_, leases, _, _ = store.selectForRoute([]candidate{c}, now.Add(providerModelCooldownBase))
	token = leases[key]
	store.success(key, token)
	if _, blocked := store.blocked(key, now.Add(providerModelCooldownBase)); blocked {
		t.Fatal("a successful probe did not clear the cooldown")
	}
	if got, leases, _, cooled := store.selectForRoute([]candidate{c}, now.Add(providerModelCooldownBase)); len(got) != 1 || len(leases) != 0 || cooled {
		t.Fatalf("a cleared pair is a normal candidate: got %d leases %d cooled %v", len(got), len(leases), cooled)
	}
}

func TestGroupProvider429CoolsOnlyFailedModel(t *testing.T) {
	fresh(t)
	var mu sync.Mutex
	asked := map[string]int{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		model := modelOf(body)
		mu.Lock()
		asked[model]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if model == "bad" {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"message":"limited"}}`)
			return
		}
		io.WriteString(w, chatOK)
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "a", Name: "A", Key: "k", Models: []string{"bad", "good"}, Chat: up.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"a/bad", "a/good"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	s := New()
	body := `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`
	for request := 0; request < 2; request++ {
		if code, reply := postAs(t, s, "", body); code != http.StatusOK || !strings.Contains(reply, "hello") {
			t.Fatalf("request %d: %d %s", request+1, code, reply)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if asked["bad"] != 1 || asked["good"] != 2 {
		t.Fatalf("asked: %#v, want bad once and good twice", asked)
	}
}

func TestGroupProvider429StreamErrorFailsOverAndCools(t *testing.T) {
	fresh(t)
	limited := sse(`event: error` + "\n" + `data: {"type":"error","error":{"type":"rate_limit_error","message":"limited"}}`)
	good := sse(`event: message_start`+"\n"+`data: {"type":"message_start","message":{"id":"m2","role":"assistant","content":[]}}`,
		`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"from b"}}`,
		`event: message_stop`+"\n"+`data: {"type":"message_stop"}`)
	a := &scripted{replies: []reply{{200, "text/event-stream", limited}}}
	b := &scripted{replies: []reply{{200, "text/event-stream", good}}}
	scriptedOn(t, "a", provider.Anthropic, a)
	scriptedOn(t, "b", provider.Anthropic, b)
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"a/m", "b/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	s := New()
	send := func() (int, string) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"group/g","stream":true,"max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)))
		return rec.Code, rec.Body.String()
	}
	for request := 0; request < 2; request++ {
		if code, body := send(); code != http.StatusOK || !strings.Contains(body, "from b") || strings.Contains(body, "limited") {
			t.Fatalf("request %d: %d %s", request+1, code, body)
		}
	}
	if a.n != 1 || b.n != 2 {
		t.Fatalf("asks: a %d b %d", a.n, b.n)
	}
}

func TestGroupProbeSuccessClearsCooldown(t *testing.T) {
	fresh(t)
	var asked int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		io.WriteString(w, chatOK)
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "a", Name: "A", Key: "k", Models: []string{"m"}, Chat: up.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"a/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	// The pair cooled down a moment ago: its recovery probe is due.
	provider429s.fail(providerModelKey{provider: "a", model: "m"}, 0, time.Now().Add(-20*time.Second))
	s := New()
	body := `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`
	if code, _ := postAs(t, s, "", body); code != http.StatusOK || asked != 1 {
		t.Fatalf("probe: %d (asked %d)", code, asked)
	}
	// The success cleared the cooldown: the next request asks the pair as
	// an ordinary candidate, not a recovery probe.
	if code, _ := postAs(t, s, "", body); code != http.StatusOK || asked != 2 {
		t.Fatalf("after success: %d (asked %d)", code, asked)
	}
}

func TestGroupAllProviderModelsCoolingReturns429WithoutUpstream(t *testing.T) {
	fresh(t)
	key := providerModelKey{provider: "a", model: "m"}
	provider429s.fail(key, 0, time.Now())
	var asked int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked++; io.WriteString(w, chatOK) }))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "a", Name: "A", Key: "k", Models: []string{"m"}, Chat: up.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"a/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	code, reply := postAs(t, New(), "", `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusTooManyRequests || asked != 0 || !strings.Contains(reply, "cooling down") {
		t.Fatalf("%d %s (upstream %d)", code, reply, asked)
	}
}

func TestGroupLoneCandidate429ReturnsOriginalWithoutReask(t *testing.T) {
	fresh(t)
	limited := reply{429, "", `{"error":{"message":"rate limit reached","code":"rate_limit_reached"}}`}
	a := &scripted{replies: []reply{limited}}
	scriptedOn(t, "a", provider.Chat, a)
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"a/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	code, body := postAs(t, New(), "", `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusTooManyRequests || !strings.Contains(body, "rate limit") {
		t.Fatalf("%d %s", code, body)
	}
	if a.n != 1 {
		t.Fatalf("lone 429 candidate asked %d times, want 1", a.n)
	}
}

func TestProviderModelCooldownConcurrentFailure(t *testing.T) {
	store := providerModelCooldowns{states: map[providerModelKey]providerModelState{}}
	now := time.Unix(1_700_000_000, 0)
	key := providerModelKey{provider: "a", model: "m"}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.fail(key, 0, now)
		}()
	}
	wg.Wait()
	store.Lock()
	state := store.states[key]
	store.Unlock()
	if state.failures != 4 || state.until.Sub(now) != providerModelCooldownMax {
		t.Fatalf("concurrent state: %+v", state)
	}
}
