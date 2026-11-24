# Repository Structure

Last reviewed: 2026-10-08

This repository contains `deezer-tui`, a Bubble Tea based terminal client for Deezer. The project is intentionally compact: the command entry point is thin, domain state lives under `internal/app`, external Deezer interaction is isolated in `internal/deezer`, terminal UI orchestration lives in `internal/tui`, and playback concerns live in `internal/player`.

## Top-level layout

```text
.
├── cmd/deezer-tui/        Application entry point
├── docs/                  Project notes and architecture documentation
├── images/                README preview images
├── internal/              Private application packages
├── Makefile               Build, lint, audit, and test commands
├── README.md              User-facing project overview
├── go.mod                 Go module definition
└── go.sum                 Locked dependency checksums
```

## Package map

### `cmd/deezer-tui`

The executable entry point. `main.go` creates the Bubble Tea program with `tui.New()` and handles top-level startup errors.

### `internal/app`

State and pure-ish application behavior. This package owns navigation routes, active panels, queue state, search state, repeat mode, now-playing metadata, and high-level command kinds. It is the best first stop when changing how user intent maps onto application state.

### `internal/config`

Configuration loading, saving, defaults, and validation. The app reads `~/.deezer-tui-config.json`, requires a Deezer `arl`, and fills missing values with defaults.

### `internal/colorscheme`

Theme definitions and lookup/normalization logic. The README currently calls out Aetheria, Gruvbox, and Winamp as supported themes.

### `internal/deezer`

Deezer HTTP/API integration. This package handles ARL-backed client setup, cookies, API tokens, playlist/search/metadata fetching, media URL resolution, stream opening, and Deezer-specific crypto.

### `internal/player`

Playback pipeline and platform-independent playback interfaces. It resolves metadata, obtains signed media streams, decrypts/buffers them, and hands playback to a backend. The package also contains stream buffering and visualization support.

Platform-specific backend files live here:

```text
internal/player/process_backend_darwin.go
internal/player/process_backend_other.go
```

### `internal/tui`

Bubble Tea model, update/view logic, loading orchestration, artwork rendering, and playback runtime glue. Platform-specific playback integration also lives here:

```text
internal/tui/playback_darwin.go
internal/tui/playback_other.go
internal/tui/mac_player_helper.swift
```

The Swift helper supports macOS native playback/media integration and is compiled during `make build` on Darwin.

## Build and verification

Common commands from the Makefile:

```bash
make build
make test
make lint
make audit
```

Development run:

```bash
go run ./cmd/deezer-tui
```

The test suite is colocated with the packages it covers, for example:

```text
internal/app/state_test.go
internal/config/config_test.go
internal/deezer/client_test.go
internal/player/pipeline_test.go
internal/tui/model_test.go
```

## Reflection notes

- The package boundaries are already pretty healthy: state, TUI orchestration, Deezer access, and playback are separated in ways that make targeted changes possible.
- `internal/tui` is the busiest package. Future docs would help most around message flow, playback lifecycle, and prebuffering behavior.
- `internal/player` has clear interfaces (`MediaClient`, `Backend`, `Controller`) that make it a good place to document runtime contracts before adding more platforms.
- The README is user-facing and feature-oriented. `docs/` should stay developer-facing: structure, architecture decisions, smoke tests, and implementation notes.

