# Routing configuration UI

This reference describes where routing configuration lives in the window: which
control configures which routing decision, and how the two routing homes — the
Routing page and the Provider editor — are laid out. It covers presentation
only. What the controls configure is in [gateway-routing.md](gateway-routing.md),
and the shell's own rendering rules are in [gui-shell.md](gui-shell.md).

## The three routing decisions

A request for a model is routed by answering three "which" questions, each
configured in its own place:

1. **Which provider (or group)?** — across providers. Configured on the
   Routing page: routing groups, found groups, and each provider's
   cross-provider fallback.
2. **Which model, and how does it fail over?** — within one provider, by the
   provider's own routing strategy: model-family fallback, per-model cooldown,
   upstream throttle, reply classification and pre-truncation. Configured in the
   Provider editor as *Routing strategy* (`RoutePolicy`, the
   `routes/<id>.json` file).
3. **Which key or account?** — within one provider. Configured in the
   Provider editor as *Keys & accounts* (`Provider.Routing`,
   `Provider.Affinity`, `Provider.Sink`, and per-key weights).

Decisions 2 and 3 are provider-internal, so they live in the Provider editor;
decision 1 is cross-provider, so it lives on the Routing page. In the routing
path, the model-family chain is injected ahead of the cross-provider fallback
(`planRoute` in [`route_fallback.go`](../../internal/gateway/route_fallback.go)),
and the key or account is then chosen within the provider
(`weigh`/`routingOf` in [`routing.go`](../../internal/gateway/routing.go)).

## The two homes

### Routing page (cross-provider)

- **Routing groups** (`Group`): members, match patterns, rules, classifier,
  effort, weights, affinity, sink, routing, first token, patience, fast mode,
  context, levels, and unlisting.
- **Found groups** and **Templates**.
- **Pools**: providers with several keys or accounts, showing their routing
  mode; the controls jump into the Provider editor to change it.
- The **live routing trace** and **Try now** (`POST /api/gateway/unrest`).

Backed by [`groups.go`](../../internal/gui/groups.go),
[`trace.go`](../../internal/gui/trace.go), and
[`plugins/routing/client.js`](../../internal/gui/assets/plugins/routing/client.js).

### Provider editor (provider-internal)

The editor is a main list of connection and model essentials, plus four folds:

- **Main** (always open): the API key, server address, base URL, region,
  endpoints, accounts/keys, the model list and image models, team/access keys,
  the System One/decision endpoint, the icon, and Step Plan allowances.
- **Routing**: the provider's own routing — *Keys & accounts*, *Fallback*, and
  *Routing strategy*.
- **Model description**: context window, outputs, and compaction (native
  compaction where the provider carries it).
- **Network & limits**: proxy, concurrency, price rate, and rests.
- **Advanced**: the remaining request details — extra headers, extra body, key
  header, User-Agent, redaction, web search, the account-balance token, and a
  Cline provider's upstream pin.

A custom provider's *More endpoints* fold (protocol URLs, models URL, catalog,
balance URL) stays under the main list.

## Naming

The word "Routing" named two things at once — the cross-provider page and the
provider's key/account spread field — so the field was renamed to say what it
does:

| Before | After | What it is |
| --- | --- | --- |
| Routing (page) | Routing (unchanged) | the cross-provider overview |
| Routing (editor field) | Keys & accounts | how requests spread over the provider's keys or accounts |

## Sources of truth

- Editor assembly: [`internal/gui/assets/app.js`](../../internal/gui/assets/app.js)
  — `renderRouting` (Keys & accounts), `renderFallback` (Fallback), `routeField`
  (Routing strategy), `renderModels`, `renderAccounts`/`renderKeyAccounts`, and
  the `ed` and fold (`rout`, `modelDesc`, `netLim`, `adv`) assembly.
- Routing page: [`internal/gui/assets/plugins/routing/client.js`](../../internal/gui/assets/plugins/routing/client.js).
- JSON API: [`internal/gui/groups.go`](../../internal/gui/groups.go),
  [`internal/gui/providers.go`](../../internal/gui/providers.go),
  [`internal/gui/trace.go`](../../internal/gui/trace.go).
- Data the controls write: `Provider.Routing`/`Affinity`/`Sink`/`Fallback`/
  `Weights`, `RoutePolicy` (`routes/<id>.json`), and `Group` — see
  [gateway-routing.md](gateway-routing.md) and
  [providers-accounts.md](providers-accounts.md).

## Verification

The shell is covered by node tests under `internal/gui/tests/*.test.cjs` (run
with `make test-ui`) and by `go build ./...` / `go vet ./...` for the Go
handlers. The editor layout moves and renames controls only: it writes no new
fields and changes no gateway routing logic, so the routing behavior in
[gateway-routing.md](gateway-routing.md) is as that page describes.
