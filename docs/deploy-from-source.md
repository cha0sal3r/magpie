# Running a source build as a background service

How to build magpie from this repository, put the command on `PATH`, and run
it as a persistent gateway service (with the web UI) on a server or a machine
you reach over SSH. The full service reference is
[As a background service](reference.md#as-a-background-service); the make
targets are in [Install](reference.md#install).

## 1 · Build

```sh
make cli     # terminal-only, no cgo: everything `magpie serve` and `daemon` need
```

`make build` (or `make app` on macOS) adds the desktop app, at the cost of
cgo and the platform webview. A headless service only needs `make cli`, which
leaves `./magpie` in the repository.

## 2 · Put the command on PATH — link it, don't copy it

`go install github.com/yetone/magpie@latest` installs to `GOBIN`. If you
instead place a `magpie` command in `~/.local/bin` (or `$MAGPIE_BIN_DIR`)
yourself, make it a **symlink**, not a plain copied file.

On macOS magpie checks that path, and when it is a copy rather than a link the
window and the web UI advise that the command can't follow updates
(`internal/update/relink.go`'s `CopiedCLI`). A copy placed there by hand — an
older installer or a `cp` — keeps running its own build after the app moves
on. Link it instead:

```sh
ln -sf /path/to/built/magpie ~/.local/bin/magpie
```

`magpie daemon install` runs the command it was started from by the name on
`PATH` when that is the same file, so a link also keeps the service pointed at
the binary you rebuild in place.

## 3 · Run as a service

```sh
magpie daemon install       # launch agent (macOS) / systemd --user unit (Linux)
magpie daemon web on        # also serve the web UI; `magpie daemon status` prints the link
```

The service starts at login, restarts itself when it stops with a failure,
and runs `magpie serve` with no window. `--web`, `--lan`, `--addr`, TLS and
the per-platform notes are in the reference linked above.

## 4 · Rebuild

`make cli` overwrites `./magpie` in place; restart the service to load it:

```sh
make cli && magpie daemon restart
```

Don't `make clean` a binary that a linked command or service points at — it
removes the target the link resolves to.
