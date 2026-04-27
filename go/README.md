# Go Rewrite

This directory is the parallel Go rewrite of the Rust `deezer-tui` codebase.

Current scope:
- `internal/config`: config and audio-quality model
- `internal/app`: deterministic app state, queue behavior, Flow pagination state, and navigation logic
- `internal/deezer`: Deezer auth/session bootstrap, gateway/API loaders, media URL lookup, and crypto helpers
- `internal/player`: stream buffer primitive for the future player backend
- `internal/tui`: first Bubble Tea shell over the Go app state
- `cmd/deezer-tui`: runnable Go entrypoint

What is intentionally not ported yet:
- audio sink / playback backend
- MPRIS
- Discord Rich Presence
- cover-art/image protocol support

Why start here:
- the state machine is the most testable part of the app
- Flow behavior has already accumulated edge-case fixes in Rust
- porting the deterministic core first reduces regression risk before moving on to network and audio

Toolchain note:
- local verification currently runs with `go1.25.9`
- target runtime/toolchain for the rewrite is `go1.26.2`

Build and run from the repository root:

```bash
make build
./target/release/deezer-tui
```

Useful checks:

```bash
make lint
make audit
make test
```

Planned next phases:
1. choose and wire the Go audio backend around `internal/player`
2. connect the Bubble Tea shell to the Deezer loaders and playback pipeline
3. flesh out queue/search/settings interactions in the Go UI
4. port MPRIS and Discord integrations
