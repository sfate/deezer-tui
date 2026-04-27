package player

import (
	"bytes"
	"context"
	"crypto/cipher"
	"io"
	"sync"
	"testing"

	"deezer-tui-go/internal/deezer"
	"golang.org/x/crypto/blowfish"
)

type fakeMediaClient struct {
	meta   deezer.TrackMetadata
	stream []byte
}

func (f *fakeMediaClient) FetchAPIToken(context.Context) (string, error) {
	return "token", nil
}

func (f *fakeMediaClient) FetchTrackMetadata(context.Context, string) (deezer.TrackMetadata, error) {
	return f.meta, nil
}

func (f *fakeMediaClient) FetchMediaURL(context.Context, deezer.MediaRequest) (string, error) {
	return "signed-url", nil
}

func (f *fakeMediaClient) OpenSignedStream(context.Context, string) (io.ReadCloser, int64, error) {
	return io.NopCloser(bytes.NewReader(f.stream)), int64(len(f.stream)), nil
}

type fakeBackend struct {
	mu         sync.Mutex
	playedData []byte
	stopped    bool
}

func (b *fakeBackend) Start(stream io.ReadSeeker, onNaturalStop func()) (Controller, error) {
	go func() {
		data, err := io.ReadAll(stream)
		if err != nil {
			return
		}

		b.mu.Lock()
		b.playedData = append([]byte(nil), data...)
		b.mu.Unlock()

		onNaturalStop()
	}()
	return &fakeController{backend: b}, nil
}

type fakeController struct {
	backend *fakeBackend
}

func (c *fakeController) Pause()              {}
func (c *fakeController) Resume()             {}
func (c *fakeController) SetVolume(v float32) {}
func (c *fakeController) Stop() {
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()
	c.backend.stopped = true
}

func TestStartTrackPipelineDecryptsAndStreamsAudio(t *testing.T) {
	plaintext := bytes.Repeat([]byte{0x5a}, deezerChunkSizeForTest()*4)
	encrypted := encryptDeezerChunks(t, "42", plaintext)

	client := &fakeMediaClient{
		meta: deezer.TrackMetadata{
			ID:         "42",
			Title:      "Track",
			Artist:     "Artist",
			TrackToken: "token",
		},
		stream: encrypted,
	}
	backend := &fakeBackend{}

	var changed bool
	var stopped bool
	var progressCurrent uint64
	var progressTotal uint64

	session := StartTrackPipeline(
		context.Background(),
		client,
		backend,
		"42",
		deezer.AudioQuality320,
		0,
		EventHandler{
			OnTrackChanged: func(meta deezer.TrackMetadata, quality deezer.AudioQuality, initialMS uint64) {
				changed = meta.ID == "42" && quality == deezer.AudioQuality320 && initialMS == 0
			},
			OnPlaybackProgress: func(currentMS, totalMS uint64) {
				progressCurrent = currentMS
				progressTotal = totalMS
			},
			OnPlaybackStopped: func() {
				stopped = true
			},
		},
		PipelineOptions{
			PrebufferBytes: 1024,
			ChunkSize:      deezerChunkSizeForTest(),
		},
	)

	if err := session.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	if !changed {
		t.Fatalf("expected track-changed event")
	}
	if !stopped {
		t.Fatalf("expected playback-stopped event")
	}
	if progressCurrent != 0 || progressTotal == 0 {
		t.Fatalf("unexpected progress event current=%d total=%d", progressCurrent, progressTotal)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !bytes.Equal(backend.playedData, plaintext) {
		t.Fatalf("backend received decrypted bytes mismatch")
	}
}

func TestStartTrackPipelineSeeksByDroppingInitialBytes(t *testing.T) {
	plaintext := bytes.Repeat([]byte("abcd"), 1024)
	encrypted := encryptDeezerChunks(t, "42", plaintext)

	client := &fakeMediaClient{
		meta: deezer.TrackMetadata{
			ID:           "42",
			Title:        "Track",
			Artist:       "Artist",
			TrackToken:   "token",
			DurationSecs: uint64Ptr(4),
		},
		stream: encrypted,
	}
	backend := &fakeBackend{}

	session := StartTrackPipeline(
		context.Background(),
		client,
		backend,
		"42",
		deezer.AudioQuality320,
		2000,
		EventHandler{},
		PipelineOptions{
			PrebufferBytes: 1,
			ChunkSize:      deezerChunkSizeForTest(),
		},
	)

	if err := session.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.playedData) >= len(plaintext) {
		t.Fatalf("expected seek to reduce played bytes")
	}
	if bytes.Equal(backend.playedData, plaintext) {
		t.Fatalf("expected seeked playback to differ from full plaintext")
	}
}

func encryptDeezerChunks(t *testing.T, trackID string, plaintext []byte) []byte {
	t.Helper()
	key := deezer.DeriveBlowfishKey(trackID)
	encrypted := append([]byte(nil), plaintext...)

	block, err := blowfish.NewCipher(key[:])
	if err != nil {
		t.Fatalf("new blowfish cipher: %v", err)
	}

	chunkSize := deezerChunkSizeForTest()
	for chunkIndex, start := 0, 0; start < len(encrypted); chunkIndex, start = chunkIndex+1, start+chunkSize {
		end := start + chunkSize
		if end > len(encrypted) {
			end = len(encrypted)
		}
		if chunkIndex%3 != 0 {
			continue
		}
		chunk := encrypted[start:end]
		decryptableLen := len(chunk) - (len(chunk) % 8)
		if decryptableLen == 0 {
			continue
		}
		cipher.NewCBCEncrypter(block, []byte{0, 1, 2, 3, 4, 5, 6, 7}).CryptBlocks(chunk[:decryptableLen], chunk[:decryptableLen])
	}

	return encrypted
}

func deezerChunkSizeForTest() int {
	return 2048
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}
