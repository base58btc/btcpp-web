// Package secureblob stores small encrypted blobs in Spaces.
package secureblob

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"btcpp-web/external/spaces"
)

const contentType = "application/octet-stream"

// DecodeKey accepts a base64-encoded 32-byte AES key.
func DecodeKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("missing encryption key")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

func Encrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return out, nil
}

func Decrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext shorter than nonce")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	body := ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}

func Load(key string, encryptionKey []byte) ([]byte, bool, error) {
	if key == "" {
		return nil, false, fmt.Errorf("missing blob key")
	}
	if !spaces.Exists(key) {
		return nil, false, nil
	}
	encrypted, err := spaces.Get(key)
	if err != nil {
		return nil, false, err
	}
	plain, err := Decrypt(encrypted, encryptionKey)
	if err != nil {
		return nil, true, err
	}
	return plain, true, nil
}

func Save(key string, plaintext, encryptionKey []byte) error {
	if key == "" {
		return fmt.Errorf("missing blob key")
	}
	encrypted, err := Encrypt(plaintext, encryptionKey)
	if err != nil {
		return err
	}
	return spaces.PutPrivate(key, encrypted, contentType)
}

func Delete(key string) error {
	if key == "" {
		return fmt.Errorf("missing blob key")
	}
	return spaces.DeletePrivate(key)
}
