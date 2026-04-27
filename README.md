![Deezer TUI Preview](images/preview.png)
> [!IMPORTANT]  
> This project was heavily "vibecoded" with AI. I put it together quickly because I just wanted a simple deezer TUI for my own personal use.

## Controls

    Arrow Keys / HJKL - Navigate

    TAB - Switch Focus

    Enter - Select

    P / Space - Play/Pause

    / - Search

    Q - Quit

## Features
- Browse your Playlists, Favorites
- Explore, Home feed
- Search for tracks, albums, artists, and playlists
- Queue
- Album Artwork support (kitty and ueberzugpp has full quality)
- Multiple quality options for streaming (128kbps, 320kbps, and FLAC)
- Discord Rich Presence support
- Cross Fade support (configurable in settings)
- [ARL login](https://www.dumpmedia.com/deezplus/deezer-arl.html#part2)

## Go Rewrite

The active rewrite is under [go/](./go) and uses Bubble Tea for the TUI.

Current status:
- the Go app builds and runs
- the Bubble Tea shell is in place
- Deezer client, Flow logic, and playback pipeline scaffolding are ported
- feature parity with the Rust app is not complete yet

## Build From Source

Requirements:
- Go `1.25.9` or newer for local development

Build the Go binary from the repository root:

```bash
make build
```

This produces:

```bash
./target/release/deezer-tui
```

Run it with:

```bash
./target/release/deezer-tui
```

For a direct dev run without creating the release binary:

```bash
go -C go run ./cmd/deezer-tui
```

Useful development commands:

```bash
make lint
make audit
make test
```

## Installation

<details>
<summary><b>Arch Based (AUR)</b></summary>
<br>

Available in the AUR as `deezer-tui-bin`. You can install it using your favorite AUR helper like `paru` or `yay`:

```bash
paru -S deezer-tui-bin
```

</details>

<details>
<summary><b>Ubuntu & Debian</b></summary>

    Head over to the (Releases page)[https://github.com/Minuga-RC/deezer-tui/releases] and download the latest .deb file.

    Open your terminal in your downloads folder and run:

Bash

sudo apt install ./deezer-tui_*_amd64.deb

</details>

<details>
<summary><b>Other Linux (Standalone Binary)</b></summary>

If you aren't on Arch or Debian, you can just grab the pre-compiled binary from the Releases page, make it executable, and run it directly!

</details>

## Pick Up This Project

Since I originally made this just to fit my own needs, I don't plan on actively maintaining or expanding it. If you stumble across this and want to pick up the project, clean up the code, or add new features please do!
