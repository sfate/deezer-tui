package deezer

import (
	"crypto/cipher"
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/blowfish"
)

func TestDerivedKeyIsStable(t *testing.T) {
	key := DeriveBlowfishKey("3135556")
	if got := hex.EncodeToString(key[:]); got != "6c6c666b39662c37652575603c643439" {
		t.Fatalf("unexpected derived key %q", got)
	}
}

func TestDecryptsOnlyEveryThirdChunk(t *testing.T) {
	key := DeriveBlowfishKey("42")
	plaintext := make([]byte, chunkSize*4)
	for i := range plaintext {
		plaintext[i] = 0x5a
	}
	encrypted := append([]byte(nil), plaintext...)

	block, err := blowfish.NewCipher(key[:])
	if err != nil {
		t.Fatalf("new blowfish cipher: %v", err)
	}

	for _, chunkIndex := range []int{0, 3} {
		start := chunkIndex * chunkSize
		end := start + chunkSize
		chunk := encrypted[start:end]
		cipher.NewCBCEncrypter(block, deezerBlowfishIV).CryptBlocks(chunk, chunk)
	}

	decrypted, err := DecryptAudioStreamWithKey(key[:], encrypted)
	if err != nil {
		t.Fatalf("decrypt audio stream: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Fatalf("decrypted bytes do not match plaintext")
	}
}

func TestBuildsStableCDNURL(t *testing.T) {
	url, err := BuildCDNURL("0123456789abcdef0123456789abcdef", "3135556", 3, "1")
	if err != nil {
		t.Fatalf("build cdn url: %v", err)
	}

	const want = "https://f-cdnt-stream.dzcdn.net/mobile/1/5106275779699c6475da81bd1e291387ad490ae2b5d16f20391b487b77a80a89c0efbb341173e62e6c9a982b1267afb619cf52ab2fc1386eb8629b2e9ba88a55055f121500d83c92af111892de3d9edf393b413b3be2ee27eb61e8a66b5c0705"
	if url != want {
		t.Fatalf("unexpected cdn url\n got: %s\nwant: %s", url, want)
	}
}
