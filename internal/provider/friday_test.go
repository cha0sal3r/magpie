package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestFridayPresetUsesLocalAdapter(t *testing.T) {
	pr := Preset("friday")
	if pr == nil {
		t.Fatal("Friday preset is missing")
	}
	if pr.Kind != KindLocal || !pr.NoKey || pr.Chat != "http://127.0.0.1:18082/v1" {
		t.Fatalf("Friday preset: %+v", pr)
	}
	if !slices.Contains(pr.HeaderHints, "X-Friday-Bridge-Token") {
		t.Fatalf("Friday header hints: %v", pr.HeaderHints)
	}
	p, err := FromPreset("friday")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Ready() || !slices.Equal(p.Speaks(), []Protocol{Chat}) {
		t.Fatalf("Friday provider ready=%v speaks=%v", p.Ready(), p.Speaks())
	}
	if p.Key != "" || len(p.Headers) != 0 || p.Responses != "" || p.Anthropic != "" {
		t.Fatalf("Friday unexpectedly has credentials or unsupported endpoints: %+v", p)
	}
}

func TestFridayBridgeHeaderForRequestsAndModels(t *testing.T) {
	var paths []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("X-Friday-Bridge-Token") != "private-test-token" {
			http.Error(w, "missing bridge token", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-chat"}]}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer up.Close()
	p, err := FromPreset("friday")
	if err != nil {
		t.Fatal(err)
	}
	p.Chat = up.URL + "/v1"
	p.Headers = map[string]string{"X-Friday-Bridge-Token": "private-test-token"}
	ms, err := p.Fetch(context.Background())
	if err != nil || len(ms) != 1 || ms[0].ID != "test-chat" {
		t.Fatalf("fetch Friday models: %v, %+v", err, ms)
	}
	req, err := http.NewRequest(http.MethodPost, up.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Sign(context.Background(), req, Chat, nil); err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || !slices.Contains(paths, "/v1/models") || !slices.Contains(paths, "/v1/chat/completions") {
		t.Fatalf("Friday bridge status=%d paths=%v", res.StatusCode, paths)
	}
}
