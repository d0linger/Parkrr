package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// newAEAD derives an AES-256-GCM cipher from the application session secret.
// A fixed context string domain-separates this key from other uses.
func newAEAD(secret string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("parkrr-totp-v1:" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encrypt seals plaintext with AES-GCM and returns base64(nonce||ciphertext).
func (m *Manager) encrypt(plaintext string) (string, error) {
	if m.aead == nil {
		return "", errors.New("encryption not configured")
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := m.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// decrypt reverses encrypt.
func (m *Manager) decrypt(encoded string) (string, error) {
	if m.aead == nil {
		return "", errors.New("encryption not configured")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	ns := m.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := m.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// EncryptTOTPSecret encrypts a plaintext TOTP secret for storage.
func (m *Manager) EncryptTOTPSecret(plaintext string) (string, error) {
	return m.encrypt(plaintext)
}

// Seal encrypts arbitrary short-lived state (e.g. a WebAuthn ceremony challenge)
// for storage in a cookie. Open reverses it.
func (m *Manager) Seal(plaintext string) (string, error) { return m.encrypt(plaintext) }

// Open decrypts a value produced by Seal.
func (m *Manager) Open(ciphertext string) (string, error) { return m.decrypt(ciphertext) }

// ValidateEncryptedTOTP decrypts the stored secret and checks the code.
func (m *Manager) ValidateEncryptedTOTP(encoded, code string) bool {
	secret, err := m.decrypt(encoded)
	if err != nil {
		return false
	}
	return ValidateTOTP(secret, code)
}
