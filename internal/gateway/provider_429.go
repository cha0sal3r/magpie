package gateway

import (
	"sync"
	"time"
)

const (
	providerModelCooldownBase      = 10 * time.Second
	providerModelCooldownMax       = time.Minute
	providerModelCooldownRetention = 10 * time.Minute
	providerModelProbeLease        = 10 * time.Second
)

type providerModelKey struct {
	provider string
	model    string
}

type providerModelState struct {
	failures int
	until    time.Time
	last429  time.Time
	probe    uint64
	lease    time.Time
}

type providerModelCooldowns struct {
	sync.Mutex
	states map[providerModelKey]providerModelState
	next   uint64
}

var provider429s = providerModelCooldowns{states: map[providerModelKey]providerModelState{}}

func providerModelOf(c candidate) providerModelKey {
	return providerModelKey{provider: c.p.ID, model: c.model}
}

// fail records an upstream 429 for one provider and concrete requested model.
func (s *providerModelCooldowns) fail(key providerModelKey, token uint64, now time.Time) (time.Time, int) {
	if key.provider == "" || key.model == "" {
		return time.Time{}, 0
	}
	s.Lock()
	defer s.Unlock()
	if s.states == nil {
		s.states = map[providerModelKey]providerModelState{}
	}
	state := s.states[key]
	if token != 0 && state.probe != token {
		return state.until, state.failures // a newer probe or failure owns the pair
	}
	if now.Sub(state.last429) > providerModelCooldownRetention {
		state.failures = 0
	}
	state.failures = min(state.failures+1, 4)
	d := min(providerModelCooldownBase<<max(state.failures-1, 0), providerModelCooldownMax)
	state.until, state.last429 = now.Add(d), now
	state.probe, state.lease = 0, time.Time{}
	s.states[key] = state
	return state.until, state.failures
}

// unrest deletes provider/model cooldowns named "provider/model" or
// "provider", the latter clearing every model when the provider is reset.
func (s *providerModelCooldowns) unrest(id string, all bool) int {
	s.Lock()
	defer s.Unlock()
	n := 0
	for key := range s.states {
		if key.provider+"/"+key.model == id || all && key.provider == id {
			delete(s.states, key)
			n++
		}
	}
	return n
}

func (s *providerModelCooldowns) success(key providerModelKey, token uint64) {
	if key.provider == "" || key.model == "" {
		return
	}
	s.Lock()
	if state, ok := s.states[key]; ok && (token == 0 || state.probe == token) {
		delete(s.states, key)
	}
	s.Unlock()
}

// blocked reports whether a provider-model is cooling or has an active probe.
func (s *providerModelCooldowns) blocked(key providerModelKey, now time.Time) (time.Time, bool) {
	s.Lock()
	defer s.Unlock()
	state, ok := s.states[key]
	if !ok {
		return time.Time{}, false
	}
	if now.Before(state.until) || state.probe != 0 && now.Before(state.lease) {
		until := state.until
		if state.lease.After(until) {
			until = state.lease
		}
		return until, true
	}
	return time.Time{}, false
}

// selectForRoute omits cooling pairs from automatic routing. Once a cooldown
// ends it keeps one candidate as the pair's recovery probe and leases that
// probe for ten seconds, so concurrent requests continue on healthy pairs.
func (s *providerModelCooldowns) selectForRoute(cs []candidate, now time.Time) ([]candidate, map[providerModelKey]uint64, time.Time, bool) {
	s.Lock()
	defer s.Unlock()
	out := make([]candidate, 0, len(cs))
	leases := map[providerModelKey]uint64{}
	seen := map[providerModelKey]bool{}
	var back time.Time
	cooled := false
	for _, c := range cs {
		key := providerModelOf(c)
		state, ok := s.states[key]
		if !ok {
			out = append(out, c)
			continue
		}
		if now.Sub(state.last429) > providerModelCooldownRetention && !now.Before(state.until) && (state.probe == 0 || !now.Before(state.lease)) {
			delete(s.states, key)
			out = append(out, c)
			continue
		}
		if now.Before(state.until) || state.probe != 0 && now.Before(state.lease) || seen[key] {
			cooled = true
			until := state.until
			if state.probe != 0 && state.lease.After(until) {
				until = state.lease
			}
			if back.IsZero() || until.Before(back) {
				back = until
			}
			continue
		}
		// The cooldown ended. Admit one key/account of this provider-model;
		// the channel/model state intentionally spans all of its credentials.
		s.next++
		state.probe, state.lease = s.next, now.Add(providerModelProbeLease)
		s.states[key] = state
		leases[key], seen[key] = state.probe, true
		out = append(out, c)
	}
	return out, leases, back, cooled
}

// release gives back a recovery probe that did not reach a conclusive upstream
// result. A generation token prevents an old request releasing newer state.
func (s *providerModelCooldowns) release(key providerModelKey, token uint64, now time.Time) {
	if token == 0 {
		return
	}
	s.Lock()
	state, ok := s.states[key]
	if ok && state.probe == token {
		state.probe, state.lease, state.until = 0, time.Time{}, now
		s.states[key] = state
	}
	s.Unlock()
}
