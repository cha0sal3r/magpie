package provider

import (
	"bytes"
	"os"
	"testing"
)

// The strategy file round-trips, is normalized with the defaults, and an
// off strategy with nothing set leaves no file.
func TestRouteSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	r := RoutePolicy{Enabled: true, Throttle: RouteThrottle{MaxConcurrent: 9}}
	if err := SaveRoute("relay", r); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadRoute("relay")
	if !ok {
		t.Fatal("load")
	}
	if !got.Enabled || got.Throttle.MaxConcurrent != 9 {
		t.Fatalf("got %+v", got)
	}
	// the fields the file did not name came back as the defaults.
	if got.Throttle.SendIntervalMs != DefaultRoute().Throttle.SendIntervalMs {
		t.Fatalf("send interval default not filled: %d", got.Throttle.SendIntervalMs)
	}
	if len(got.Family.Families) != 0 {
		t.Fatal("a generic default must not inject model families")
	}
	// the file itself was written normalized, not just the fields given, and
	// carries the self-documenting comment header.
	if b, err := os.ReadFile(RoutePath("relay")); err != nil || len(b) == 0 {
		t.Fatalf("file %v %q", err, b)
	} else if !bytes.Contains(b, []byte("// Routing strategy for this provider")) {
		t.Fatalf("file lacks the comment header: %q", b)
	}

	// off with nothing set: the file is removed.
	if err := SaveRoute("relay", RoutePolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadRoute("relay"); ok {
		t.Fatal("off should leave no file")
	}
	if _, err := os.Stat(RoutePath("relay")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}

// A hand-written file with comments and a trailing comma loads.
func TestRouteLoadsCommentedFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	doc := `// a hand-written strategy: comments and a trailing comma are fine.
{
  "enabled": true,
  "throttle": {"maxConcurrent": 4,},
}`
	if err := os.MkdirAll(RouteDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RoutePath("commented"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	r, ok := LoadRoute("commented")
	if !ok || !r.Enabled || r.Throttle.MaxConcurrent != 4 {
		t.Fatalf("load commented: %+v ok=%v", r, ok)
	}
}

// A provider without a file runs no strategy, whatever its preset; a file
// turns one on.
func TestRouteOfUsesFileOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, ok := RouteOf(Provider{ID: "relay", Preset: "relay"}); ok {
		t.Fatal("relay with no file should have no strategy")
	}
	if _, ok := RouteOf(Provider{ID: "openai"}); ok {
		t.Fatal("openai with no file should have no strategy")
	}
	if err := SaveRoute("relay", RoutePolicy{Enabled: true, Throttle: RouteThrottle{MaxConcurrent: 3}}); err != nil {
		t.Fatal(err)
	}
	if r, ok := RouteOf(Provider{ID: "relay", Preset: "relay"}); !ok || r.Throttle.MaxConcurrent != 3 {
		t.Fatalf("file override: %+v ok=%v", r, ok)
	}
}
