package fridaybridge

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testBridge(t *testing.T, handler http.Handler) *Bridge {
	t.Helper()
	up := httptest.NewServer(handler)
	t.Cleanup(up.Close)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	return New(Config{RemoteHost: host, RemotePort: port, Token: "test-token"})
}

func TestConfigFromEnvRequiresHostAndToken(t *testing.T) {
	t.Setenv("FRIDAY_REMOTE_HOST", "")
	t.Setenv("FRIDAY_BRIDGE_TOKEN", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv accepted a missing host and token")
	}
	t.Setenv("FRIDAY_REMOTE_HOST", "adapter")
	t.Setenv("FRIDAY_BRIDGE_TOKEN", "token")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.RemotePort != defaultRemotePort || cfg.Addr != defaultAddr {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestBridgeRequiresToken(t *testing.T) {
	b := testBridge(t, http.NotFoundHandler())
	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"no token", "", http.StatusForbidden},
		{"wrong token", "nope", http.StatusForbidden},
		{"right token", "test-token", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			if tc.token != "" {
				req.Header.Set("X-Friday-Bridge-Token", tc.token)
			}
			b.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestBridgeRejectsUnknownPath(t *testing.T) {
	b := testBridge(t, http.NotFoundHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	req.Header.Set("X-Friday-Bridge-Token", "test-token")
	b.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestBridgeForwardsModelsAndChat(t *testing.T) {
	var gotPath, gotMethod, gotAuth, gotType, gotReqID, gotAttempt string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.RequestURI(), r.Method
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		gotReqID = r.Header.Get("X-Friday-Request-Id")
		gotAttempt = r.Header.Get("X-Friday-Attempt")
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"test-chat"}]}`)
			return
		}
		_, _ = io.WriteString(w, "chat-ok")
	}))
	defer up.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	b := New(Config{RemoteHost: host, RemotePort: port, Token: "test-token"})

	// models: GET, no body, no Authorization
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models?limit=10", nil)
	req.Header.Set("X-Friday-Bridge-Token", "test-token")
	req.Header.Set("X-Friday-Request-Id", strings.Repeat("a", 32))
	req.Header.Set("X-Friday-Attempt", "3")
	b.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotPath != "/v1/models?limit=10" || gotMethod != http.MethodGet {
		t.Fatalf("models: code=%d path=%q method=%q", rec.Code, gotPath, gotMethod)
	}
	if gotReqID != strings.Repeat("a", 32) || gotAttempt != "3" {
		t.Fatalf("models: request-id=%q attempt=%q", gotReqID, gotAttempt)
	}
	if gotAuth != "" {
		t.Fatalf("models: Authorization leaked = %q", gotAuth)
	}

	// chat: POST with a body, Authorization and Content-Type pass through
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test-chat"}`))
	req.Header.Set("X-Friday-Bridge-Token", "test-token")
	req.Header.Set("Authorization", "Bearer upstream")
	req.Header.Set("Content-Type", "application/json")
	b.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" {
		t.Fatalf("chat: code=%d method=%q path=%q", rec.Code, gotMethod, gotPath)
	}
	if gotAuth != "Bearer upstream" || gotType != "application/json" {
		t.Fatalf("chat: auth=%q type=%q", gotAuth, gotType)
	}
	if rec.Body.String() != "chat-ok" {
		t.Fatalf("chat: body = %q", rec.Body.String())
	}
}

func TestBridgeGeneratesRequestID(t *testing.T) {
	b := testBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("X-Friday-Request-Id"))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Friday-Bridge-Token", "test-token")
	b.ServeHTTP(rec, req)
	if !requestIDHex.MatchString(rec.Body.String()) {
		t.Fatalf("generated request id = %q, not 32 hex digits", rec.Body.String())
	}
}

func TestBridgeRejectsOversizedBody(t *testing.T) {
	b := testBridge(t, http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Friday-Bridge-Token", "test-token")
	req.ContentLength = maxBody + 1
	rec := httptest.NewRecorder()
	b.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestBridgeRejectsChunkedBody(t *testing.T) {
	b := testBridge(t, http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Friday-Bridge-Token", "test-token")
	req.TransferEncoding = []string{"chunked"}
	rec := httptest.NewRecorder()
	b.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestBridgeUpstreamError(t *testing.T) {
	// A bridge pointed at a closed port: the dial fails before any reply.
	b := New(Config{RemoteHost: "127.0.0.1", RemotePort: "1", Token: "test-token"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Friday-Bridge-Token", "test-token")
	b.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestServeShutsDown(t *testing.T) {
	ln, err := Listen(Config{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ln, Config{RemoteHost: "127.0.0.1", RemotePort: "1", Token: "t"})
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

// Keep the request-id matcher honest: it must reject a short or non-hex id.
func TestRequestIDHex(t *testing.T) {
	if requestIDHex.MatchString("abc") || !requestIDHex.MatchString(strings.Repeat("f", 32)) {
		t.Fatal("requestIDHex does not match 32 hex digits alone")
	}
}
