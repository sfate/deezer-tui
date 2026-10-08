# Malgo Audio Backend Plan

Issue: #28

## Summary

`github.com/gen2brain/malgo` is a Go binding for miniaudio. It is a reasonable candidate for replacing or augmenting the current Beep-based backend because it supports the major desktop audio APIs through one library:

- Windows: WASAPI, DirectSound, WinMM
- Linux: PulseAudio, ALSA, JACK
- macOS/iOS: CoreAudio
- BSD: OSS/audio(4)/sndio
- Android: OpenSL ES, AAudio

The library requires cgo. Its README says Windows and macOS do not need extra linking, while Linux/BSD link `-ldl`.

## Current Playback Split

The app already separates playback concerns:

- `internal/player.Backend` is the generic audio backend interface.
- Non-Darwin builds use `player.NewProcessBackend()`, which resolves to the Beep backend.
- Darwin builds use `internal/tui/playback_darwin.go` and the Swift helper for native playback/media controls.

This means a malgo prototype can start without replacing the macOS native media-control path.

## Recommendation

Start with a non-Darwin malgo backend prototype behind the existing `player.Backend` contract. Do not replace the Darwin runtime in the first pass.

The current prototype follows that shape:

- `internal/player/malgo_backend.go` builds only with `!darwin && cgo`.
- `internal/player/process_backend_malgo.go` selects malgo for non-Darwin cgo builds.
- `internal/player/process_backend_other.go` keeps the Beep fallback for non-Darwin no-cgo builds.
- Darwin builds remain on `internal/tui/playback_darwin.go`.

That keeps macOS media controls safe because the native Swift helper remains responsible for:

- Now Playing metadata
- media key commands
- pause/resume/volume/stop coordination
- native playback lifecycle

## Implementation Notes

The backend needs to preserve:

- `Start(stream io.ReadSeeker, quality deezer.AudioQuality, handler EventHandler, onFinished func(error))`
- `Pause`
- `Resume`
- `Stop`
- `SetVolume`
- natural finish callback behavior
- visualizer band events where practical

The most likely hard parts are decoding and buffering. The current Beep backend handles MP3/FLAC decoding and resampling. The prototype reuses the existing decode path, resamples to 48 kHz stereo, and writes float32 PCM frames into malgo.

## Risks

- cgo becomes part of the default non-Darwin audio path.
- Linux CI/build images may need confirmation that `-ldl` is available.
- Visualizer behavior may need explicit preservation because Beep currently sits in the decoded stream path.
- Full macOS migration should be a later, separate decision because it could affect native media controls.

## Verification

Run locally on macOS:

```bash
go test ./internal/player ./internal/tui ./internal/colorscheme
```

Linux+cgo should be verified in CI or on a Linux host because cross-cgo from macOS uses the macOS SDK and does not compile Linux runtime/cgo.
