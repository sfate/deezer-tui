# Go Rewrite

This directory is the parallel Go rewrite of the Rust `deezer-tui` codebase.

Current scope:
- `internal/config`: config and audio-quality model
- `internal/app`: deterministic app state, queue behavior, Flow pagination state, and navigation logic

What is intentionally not ported yet:
- Deezer HTTP/auth client
- audio streaming and decode pipeline
- TUI rendering
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

Planned next phases:
1. port Deezer auth/session/client logic
2. port playback queue engine and player control surface
3. build the Bubble Tea based TUI shell
4. port MPRIS and Discord integrations
