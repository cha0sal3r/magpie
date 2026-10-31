package gateway

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/cha0sal3r/magpie-friday/internal/provider"
)

// routeRuntime is the process-local state of one provider's routing
// strategy: its per-model cooldowns and its upstream throttle. The policy
// itself is read fresh from the provider's route file on each request, so
// an edit takes effect at once, while the cooldown and throttle state
// survives across requests.
type routeRuntime struct {
	cMu   sync.Mutex
	until map[string]time.Time // model → cooldown deadline

	sem      chan struct{}
	sendMu   sync.Mutex
	lastSend time.Time
}

// routeRuntimes keeps one runtime per provider id.
var routeRuntimes sync.Map

// routeFor returns the routing strategy a provider runs and its runtime,
// or ok=false when it runs none (no route file, or switched off).
func routeFor(p provider.Provider) (provider.RoutePolicy, *routeRuntime, bool) {
	pol, ok := provider.RouteOf(p)
	if !ok || !pol.Enabled {
		return pol, nil, false
	}
	return pol, runtimeOf(p.ID, pol.Throttle.MaxConcurrent), true
}

// routeEnabled is whether a provider runs a routing strategy.
func routeEnabled(p provider.Provider) bool {
	_, _, ok := routeFor(p)
	return ok
}

// runtimeOf returns the runtime for a provider, making one on first use.
// The throttle semaphore is sized from the policy's MaxConcurrent once, on
// creation; changing that setting takes effect after a restart.
func runtimeOf(id string, maxConcurrent int) *routeRuntime {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	v, _ := routeRuntimes.LoadOrStore(id, &routeRuntime{sem: make(chan struct{}, maxConcurrent)})
	return v.(*routeRuntime)
}

// cool sets a model's cooldown, unless the policy says it never cools, or
// a 429 (created0 is false) does not cool it.
func (rt *routeRuntime) cool(pol provider.RoutePolicy, model string, created0 bool, now time.Time) {
	if slices.Contains(pol.Cooldown.NeverCool, model) || !created0 && slices.Contains(pol.Cooldown.No429, model) {
		return
	}
	rt.cMu.Lock()
	defer rt.cMu.Unlock()
	if rt.until == nil {
		rt.until = map[string]time.Time{}
	}
	if !rt.until[model].After(now) {
		rt.until[model] = now.Add(time.Duration(pol.Cooldown.Seconds) * time.Second)
	}
}

// cooled reports whether a model is inside its cooldown.
func (rt *routeRuntime) cooled(model string, now time.Time) bool {
	rt.cMu.Lock()
	defer rt.cMu.Unlock()
	return rt.until[model].After(now)
}

// throttle waits for an upstream slot and for the policy's minimum send
// interval. It returns a function that frees the slot, or an error when ctx
// is cancelled first.
func (rt *routeRuntime) throttle(ctx context.Context, pol provider.RoutePolicy) (func(), error) {
	select {
	case rt.sem <- struct{}{}:
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
	release := func() { <-rt.sem }

	interval := time.Duration(pol.Throttle.SendIntervalMs) * time.Millisecond
	rt.sendMu.Lock()
	if wait := interval - time.Since(rt.lastSend); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			rt.sendMu.Unlock()
			release()
			return func() {}, ctx.Err()
		}
	}
	rt.lastSend = time.Now()
	rt.sendMu.Unlock()
	return release, nil
}
