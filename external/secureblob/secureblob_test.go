package secureblob

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := []byte("secret social state")

	encrypted, err := Encrypt(plain, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(encrypted, plain) {
		t.Fatalf("ciphertext contains plaintext")
	}
	got, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Decrypt = %q, want %q", got, plain)
	}
}

func TestDecodeKeyRequires32Bytes(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if _, err := DecodeKey(good); err != nil {
		t.Fatalf("DecodeKey(good): %v", err)
	}

	bad := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31))
	if _, err := DecodeKey(bad); err == nil {
		t.Fatalf("DecodeKey accepted a 31-byte key")
	}
}
