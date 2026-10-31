package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/cha0sal3r/magpie-friday/internal/provider"
)

// magpieUA is the User-Agent magpie-friday's requests go to a vendor with.
func magpieUA() string { return "magpie-friday/" + Version }

// userAgentFor is the User-Agent a request to p goes with, ua being the
// one the request came to magpie-friday with: the agent's own, as it sent it,
// where p is set to pass it on (provider.PassesUserAgent, #1851), and
// magpie-friday's where it isn't, where the agent sent none, and on magpie-friday's own
// requests — its classifier's, vision's, search's, the image tool's
// (magpiesOwn) — which no agent sent. A User-Agent the user set among the
// provider's headers is put on after, when the request is signed, and
// stands over either.
func userAgentFor(p provider.Provider, ua string) string {
	if ua != "" && p.PassesUserAgent() && !magpiesOwn(ua) {
		return ua
	}
	return magpieUA()
}

// magpiesOwn is a User-Agent magpie-friday's own requests carry: another magpie-friday's
// (magpie-friday/…), the classifier's, vision's and search's (magpie-friday-router/1,
// magpie-friday-vision/1, magpie-friday-search/1), and the image tool's, which names the
// agent it draws for before its own (claude-code/2.1.0 magpie-friday-image/1)
// from what the agent told it, not from a User-Agent the agent sent.
func magpiesOwn(ua string) bool {
	return strings.HasPrefix(ua, "magpie-friday/") || strings.HasPrefix(ua, "magpie-friday-") || strings.HasSuffix(ua, " "+DrawAgent)
}

// callerUA is the User-Agent the request ctx is for came with (withCaller),
// "" when there is none.
func callerUA(ctx context.Context) string {
	c, _ := ctx.Value(callerCtx{}).(caller)
	return c.ua
}

// stampUA puts on req, a request to p that isn't magpie-friday's own forwarded
// one (an embedding, a drawing, a video), the User-Agent forwardOnce's
// carry: magpie-friday's, or the agent's where p passes it on. A signed-in
// account's is left to its signing, as before, and another magpie-friday's is
// set where it is asked.
func stampUA(ctx context.Context, p provider.Provider, h http.Header) {
	if p.Account != nil || p.IsRemoteMagpie() {
		return
	}
	h.Set("User-Agent", userAgentFor(p, callerUA(ctx)))
}
