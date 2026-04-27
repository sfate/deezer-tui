package deezer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/blowfish"
)

const (
	deezerSecret      = "g4el58wc0zvf9na1"
	chunkSize         = 2048
	blowfishBlockSize = 8
	fieldSeparator    = "\u00a4"
)

var (
	deezerBlowfishIV = []byte{0, 1, 2, 3, 4, 5, 6, 7}
	cdnAESKey        = []byte("jo6aey6haid2Teih")
)

func DeriveBlowfishKey(trackID string) [16]byte {
	idMD5 := md5Hex([]byte(trackID))
	idMD5Bytes := []byte(idMD5)
	secretBytes := []byte(deezerSecret)
	var key [16]byte

	for i := 0; i < 16; i++ {
		key[i] = idMD5Bytes[i] ^ idMD5Bytes[i+16] ^ secretBytes[i]
	}

	return key
}

func DecryptAudioStream(trackID string, encrypted []byte) ([]byte, error) {
	key := DeriveBlowfishKey(trackID)
	return DecryptAudioStreamWithKey(key[:], encrypted)
}

func DecryptChunkedStream(trackID string, encrypted []byte) ([]byte, error) {
	return DecryptAudioStream(trackID, encrypted)
}

func DecryptChunkInPlaceWithKey(key []byte, chunkIndex int, chunk []byte) error {
	if len(key) < 4 || len(key) > 56 {
		return errors.New("invalid Blowfish key length")
	}
	if chunkIndex%3 != 0 {
		return nil
	}

	decryptableLen := len(chunk) - (len(chunk) % blowfishBlockSize)
	if decryptableLen == 0 {
		return nil
	}

	block, err := blowfish.NewCipher(key)
	if err != nil {
		return fmt.Errorf("initialize Blowfish-CBC decryptor: %w", err)
	}

	cipher.NewCBCDecrypter(block, deezerBlowfishIV).CryptBlocks(chunk[:decryptableLen], chunk[:decryptableLen])
	return nil
}

func DecryptAudioStreamWithKey(key []byte, encrypted []byte) ([]byte, error) {
	output := make([]byte, 0, len(encrypted))

	for chunkIndex, start := 0, 0; start < len(encrypted); chunkIndex, start = chunkIndex+1, start+chunkSize {
		end := start + chunkSize
		if end > len(encrypted) {
			end = len(encrypted)
		}

		decryptedChunk := append([]byte(nil), encrypted[start:end]...)
		if err := DecryptChunkInPlaceWithKey(key, chunkIndex, decryptedChunk); err != nil {
			return nil, err
		}
		output = append(output, decryptedChunk...)
	}

	return output, nil
}

func BuildCDNURL(md5Origin, songID string, format uint8, mediaVersion string) (string, error) {
	joined := fmt.Sprintf("%s%s%d%s%s%s%s", md5Origin, fieldSeparator, format, fieldSeparator, songID, fieldSeparator, mediaVersion)
	joinedHash := md5Hex([]byte(joined))
	payload := fmt.Sprintf("%s%s%s%s", joinedHash, fieldSeparator, joined, fieldSeparator)
	padded := []byte(payload)

	for len(padded)%aes.BlockSize != 0 {
		padded = append(padded, 0)
	}

	block, err := aes.NewCipher(cdnAESKey)
	if err != nil {
		return "", fmt.Errorf("initialize AES cipher: %w", err)
	}

	encrypted := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(encrypted[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}

	if len(encrypted) == 0 {
		return "", errors.New("AES-ECB encryption produced an empty CDN payload")
	}

	return "https://f-cdnt-stream.dzcdn.net/mobile/1/" + hex.EncodeToString(encrypted), nil
}

func md5Hex(bytes []byte) string {
	sum := md5.Sum(bytes)
	return hex.EncodeToString(sum[:])
}
