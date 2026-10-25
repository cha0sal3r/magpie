// Package fridaybridge is magpie's own Friday host bridge: a loopback
// HTTP server that carries the Friday Adapter's OpenAI Chat endpoint to
// the remote adapter, behind a shared token. The external Adapter owns
// Friday authentication; the bridge only moves the request over the
// host's network (a Tailscale hop a container can't make) and checks the
// shared token. magpie's gateway treats it as any other local server:
// provider preset "friday" at http://127.0.0.1:18082/v1.
package fridaybridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRemotePort = "8082"
	defaultAddr       = "127.0.0.1:18082"
	maxBody           = 32 * 1024 * 1024
)

// requestIDHex is a 32-hex-digit request id, as the Friday Router sends.
var requestIDHex = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Config is the bridge's runtime settings, read from FRIDAY_* variables.
type Config struct {
	RemoteHost string // the Friday Adapter's address
	RemotePort string // the Friday Adapter's port, "8082" by default
	Token      string // the shared token the gateway sends as X-Friday-Bridge-Token
	Addr       string // where the bridge listens, 127.0.0.1:18082 by default
}

// ConfigFromEnv reads the bridge's configuration from the environment.
// FRIDAY_REMOTE_HOST and FRIDAY_BRIDGE_TOKEN are required: the adapter's
// address and the shared token.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		RemoteHost: strings.TrimSpace(os.Getenv("FRIDAY_REMOTE_HOST")),
		RemotePort: envOr("FRIDAY_REMOTE_PORT", defaultRemotePort),
		Token:      os.Getenv("FRIDAY_BRIDGE_TOKEN"),
		Addr:       envOr("FRIDAY_BIND_ADDR", defaultAddr),
	}
	if cfg.RemoteHost == "" {
		return Config{}, errors.New("FRIDAY_REMOTE_HOST is required: the Friday Adapter's address")
	}
	if cfg.Token == "" {
		return Config{}, errors.New("FRIDAY_BRIDGE_TOKEN is required: the shared token the gateway sends")
	}
	return cfg, nil
}

// envOr is os.Getenv, with a default when the variable is empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Listen opens the bridge's listener on cfg.Addr.
func Listen(cfg Config) (net.Listener, error) {
	return net.Listen("tcp", cfg.Addr)
}

// Serve runs the bridge on ln until ctx ends, then lets requests in
// flight finish within five seconds.
func Serve(ctx context.Context, ln net.Listener, cfg Config) error {
	srv := &http.Server{Handler: New(cfg), ReadHeaderTimeout: 30 * time.Second}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-drained
		return nil
	}
	return err
}

// Bridge forwards the two Friday Adapter endpoints to the remote adapter.
type Bridge struct {
	cfg    Config
	client *http.Client
	up     string // http://host:port
}

// New builds a Bridge for cfg. The upstream connection is one per
// request (the adapter is a single remote hop), with a 320s wait for the
// response headers but no total deadline, so a stream can run as long as
// its reads keep answering.
func New(cfg Config) *Bridge {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 320 * time.Second,
		DisableKeepAlives:     true,
	}
	return &Bridge{
		cfg:    cfg,
		client: &http.Client{Transport: transport},
		up:     "http://" + net.JoinHostPort(cfg.RemoteHost, cfg.RemotePort),
	}
}

// ServeHTTP checks the shared token and routes the one endpoint magpie
// (or a Friday Router) asks for.
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.cfg.Token == "" || r.Header.Get("X-Friday-Bridge-Token") != b.cfg.Token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/health":
		b.health(w)
	case "/v1/models", "/v1/chat/completions":
		b.forward(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (b *Bridge) health(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "remote_configured": b.cfg.RemoteHost != ""})
}

func (b *Bridge) forward(w http.ResponseWriter, r *http.Request) {
	if len(r.TransferEncoding) > 0 || r.ContentLength > maxBody {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	requestID := r.Header.Get("X-Friday-Request-Id")
	if !requestIDHex.MatchString(requestID) {
		requestID = newRequestID()
	}
	attempt := 1
	if n, err := strconv.Atoi(r.Header.Get("X-Friday-Attempt")); err == nil && n >= 1 && n <= 99 {
		attempt = n
	}
	up, err := http.NewRequestWithContext(r.Context(), r.Method, b.up+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Only the fields the adapter reads go upstream; a bridge token or a
	// client's own headers stay on this hop.
	for _, h := range []string{"Content-Type", "Authorization", "OpenAI-Organization", "OpenAI-Project"} {
		if v := r.Header.Get(h); v != "" {
			up.Header.Set(h, v)
		}
	}
	up.Header.Set("X-Friday-Request-Id", requestID)
	up.Header.Set("X-Friday-Attempt", strconv.Itoa(attempt))
	resp, err := b.client.Do(up)
	if err != nil {
		http.Error(w, "Friday upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Friday-Request-Id", requestID)
	w.Header().Set("Connection", "close")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// newRequestID is a 32-hex-digit id for a request that came without one.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
