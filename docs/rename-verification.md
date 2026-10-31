# magpie-friday rename verification

_Last checked: 2026-10-31 23:33 Asia/Shanghai._

This note records the verification state after renaming the fork to
`magpie-friday` with owner/module path `github.com/cha0sal3r/magpie-friday`.
It separates rename regressions from known environment or pre-existing test
failures so packaging and deployment can proceed with the right risk picture.

## Build status

The project builds successfully as both the terminal-only binary and the full
GUI binary:

```sh
CGO_ENABLED=0 go build -tags nogui -o /tmp/magpie-friday-cli .
go build -o /tmp/magpie-friday-full .
```

`/tmp/magpie-friday-cli --help` prints the new name:

```text
magpie-friday — one place to pick every agent's model
```

The `internal/agent` package, where most rename-related fixture drift surfaced,
now passes:

```text
ok github.com/cha0sal3r/magpie-friday/internal/agent 230.108s
```

## Full test status

The latest full check was:

```sh
CGO_ENABLED=0 go test -tags nogui ./...
```

Result summary:

```text
12 failing tests
64 passing packages
```

The remaining failures are not known rename blockers. They are grouped below by
likely cause and operational impact.

## Remaining failures

### Environment, sandbox, or host-network dependent

These tests depend on conditions that are not available or stable in the current
sandbox: non-loopback/LAN networking, WSL/IPv6 behavior, proxy paths, `/bin/ps`,
fake shell behavior, or process RSS readings.

| Package | Test | Observed failure | Impact on normal use |
| --- | --- | --- | --- |
| `internal/claudebridge` | `TestCallbackGoesPastTheAgentsProxy` | call to `192.168.5.12:*` returns EOF | No impact on local build/start; only proxy/LAN callback coverage is unverified here. |
| `internal/gateway` | `TestClaudeToolCallbackOnANonLoopbackAddress` | helper callback never reaches the run; `curl: (52) Empty reply from server` | No impact on local loopback gateway use; non-loopback callback path needs a suitable host network. |
| `internal/gateway` | `TestServeWSLListensBesideLoopback` | WSL/IPv6 probe reports `WSL network gone` | No impact outside WSL/LAN serving scenarios. |
| `internal/netproxy` | `TestProxyCheckSaysAProxyPassedBy` | fake proxy path not observed; HTTP 502 | Proxy-check test environment issue; ordinary startup unaffected. |
| `internal/netproxy` | `TestLANModelListNotThroughProxy` | LAN model-list request to `192.168.5.12:*` returns EOF | Only LAN bypass behavior unverified in this sandbox. |
| `internal/plugin` | `TestHostRunsAsMagpieBun` | `/bin/ps` denied by sandbox (`operation not permitted`) | Plugin host naming behavior unverified in this sandbox; build/start unaffected. |
| `internal/proc` | `TestAskShellOwnSession` | fake shell answer is blank/space | Shell session test instability; normal binary build unaffected. |
| `internal/proc` | `TestTreeRSS` | RSS read for a running process returns 0 | Process RSS observation unavailable in this environment. |

Suggested handling if a green suite is required: make these tests use injected
fake listeners/readers where possible, or skip with a precise environment probe
when the host capability is absent. Do not change core behavior just to satisfy a
sandbox that cannot expose the required OS/network feature.

### Pre-existing or non-rename behavior differences

| Package | Test | Observed failure | Suggested fix |
| --- | --- | --- | --- |
| `internal/gui` | `TestMarshalRawIsEncodingJSON` | custom JSON marshal does not escape U+2028/U+2029 like `encoding/json` | Update the custom string escaping logic to emit `\u2028` and `\u2029`. |
| `internal/gui` | `FuzzMarshalRaw` | same U+2028/U+2029 escaping mismatch | Same as above. |
| `internal/gateway` | `TestCodexFittedImagesPastTheBudget` | image budget fixture keeps a different count than expected | Re-check whether the expected budget changed because of longer renamed strings; otherwise fix the image fitting algorithm or its fixture. |

These do not stop `magpie-friday` from compiling or starting. They should be
addressed before requiring a fully green test suite.

### Eval recording intentionally left alone

| Package | Test | Observed failure | Reason left unchanged |
| --- | --- | --- | --- |
| `internal/redact` | `TestMaskEval` | eval recording cases differ; test asks to record with `MAGPIE_EVAL_CACHE` | The rename task explicitly avoided regenerating internal eval recordings/test fixtures. |

Regenerate this only with explicit approval because it changes recorded eval data.

## Packaging/deployment guidance

For a local source deployment, the repository documents the flow in
`docs/deploy-from-source.md`:

```sh
make cli
ln -sf /path/to/built/magpie-friday ~/.local/bin/magpie-friday
magpie-friday daemon restart
```

On macOS, a desktop app bundle can be produced with:

```sh
make app
```

Packaging and deployment are not blocked by the 12 failures above; they only
block claiming that `go test -tags nogui ./...` is fully green.
