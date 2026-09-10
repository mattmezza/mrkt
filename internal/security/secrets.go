package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const secretVersion = "v1"

type SecretBox struct{ aead cipher.AEAD }

func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be exactly 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &SecretBox{a}, nil
}
func (s *SecretBox) Seal(plaintext []byte, context string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := append(nonce, s.aead.Seal(nil, nonce, plaintext, []byte(context))...)
	return secretVersion + ":" + base64.RawURLEncoding.EncodeToString(out), nil
}
func (s *SecretBox) Open(encoded, context string) ([]byte, error) {
	v, p, ok := strings.Cut(encoded, ":")
	if !ok || v != secretVersion {
		return nil, fmt.Errorf("unsupported encrypted secret version")
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted secret: %w", err)
	}
	if len(raw) < s.aead.NonceSize() {
		return nil, fmt.Errorf("encrypted secret is truncated")
	}
	nonce, ct := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ct, []byte(context))
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: authentication failed")
	}
	return plain, nil
}
