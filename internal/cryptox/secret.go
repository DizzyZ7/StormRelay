package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var ErrInvalidCiphertext = errors.New("invalid ciphertext")

type Box struct{ aead cipher.AEAD }

func NewBox(masterKey []byte) (*Box, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

func DecodeKey(value string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("decoded master key must be 32 bytes")
	}
	return key, nil
}

func (b *Box) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (b *Box) Decrypt(ciphertext, aad []byte) ([]byte, error) {
	if len(ciphertext) < b.aead.NonceSize() {
		return nil, ErrInvalidCiphertext
	}
	nonce, payload := ciphertext[:b.aead.NonceSize()], ciphertext[b.aead.NonceSize():]
	plaintext, err := b.aead.Open(nil, nonce, payload, aad)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return plaintext, nil
}

func HashSecret(secret string) [32]byte { return sha256.Sum256([]byte(secret)) }

func EqualHash(hash []byte, secret string) bool {
	candidate := HashSecret(secret)
	return len(hash) == len(candidate) && subtle.ConstantTimeCompare(hash, candidate[:]) == 1
}
