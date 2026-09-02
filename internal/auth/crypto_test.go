package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/hkdf"
)

func TestEncryptionDecryptsBoundToken(t *testing.T) {
	encryption, err := NewEncryption()
	if err != nil {
		t.Fatal(err)
	}
	token := "shed_pat_" + rawBase64.EncodeToString(bytesOf(32, 7))
	envelope := encryptForTest(t, encryption.PublicKey(), "session-123", token)

	got, err := encryption.Decrypt("session-123", envelope)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if got != token {
		t.Fatalf("Decrypt() = %q, want %q", got, token)
	}
}

func TestEncryptionRejectsTamperingAndWrongBinding(t *testing.T) {
	encryption, err := NewEncryption()
	if err != nil {
		t.Fatal(err)
	}
	token := "shed_pat_" + rawBase64.EncodeToString(bytesOf(32, 9))

	tests := []struct {
		name   string
		mutate func(*TokenEnvelope)
		id     string
	}{
		{
			name: "ciphertext",
			mutate: func(envelope *TokenEnvelope) {
				value, _ := rawBase64.DecodeString(envelope.EncryptedToken)
				value[0] ^= 0xff
				envelope.EncryptedToken = rawBase64.EncodeToString(value)
			},
			id: "session-123",
		},
		{
			name:   "session associated data",
			mutate: func(*TokenEnvelope) {},
			id:     "different-session",
		},
		{
			// An envelope format this CLI does not know how to open. It is
			// deliberately not the ceremony version: those move independently,
			// and a v2 ceremony still returns a v1 envelope.
			name: "envelope version",
			mutate: func(envelope *TokenEnvelope) {
				envelope.ProtocolVersion = "99"
			},
			id: "session-123",
		},
		{
			name: "nonce length",
			mutate: func(envelope *TokenEnvelope) {
				envelope.Nonce = rawBase64.EncodeToString([]byte("short"))
			},
			id: "session-123",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := encryptForTest(t, encryption.PublicKey(), "session-123", token)
			test.mutate(&envelope)
			if _, err := encryption.Decrypt(test.id, envelope); err == nil {
				t.Fatal("Decrypt() accepted an invalid envelope")
			}
		})
	}

	other, err := NewEncryption()
	if err != nil {
		t.Fatal(err)
	}
	envelope := encryptForTest(t, encryption.PublicKey(), "session-123", token)
	if _, err := other.Decrypt("session-123", envelope); err == nil {
		t.Fatal("Decrypt() accepted an envelope for another CLI key")
	}
}

func TestEncryptionRejectsMalformedPlaintext(t *testing.T) {
	encryption, err := NewEncryption()
	if err != nil {
		t.Fatal(err)
	}
	envelope := encryptForTest(t, encryption.PublicKey(), "session-123", "not-a-shed-token")
	_, err = encryption.Decrypt("session-123", envelope)
	if err == nil || !strings.Contains(err.Error(), "not a Shed token") {
		t.Fatalf("Decrypt() error = %v, want malformed token error", err)
	}
}

func encryptForTest(t *testing.T, clientPublicKey, sessionID, token string) TokenEnvelope {
	t.Helper()
	clientBytes, err := rawBase64.DecodeString(clientPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ecdh.X25519().NewPublicKey(clientBytes)
	if err != nil {
		t.Fatal(err)
	}
	serverPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := serverPrivate.ECDH(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	salt := bytesOf(32, 3)
	key := make([]byte, 32)
	if _, err := io.ReadFull(
		hkdf.New(sha256.New, secret, salt, []byte(protocolContext+sessionID)),
		key,
	); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytesOf(aead.NonceSize(), 5)
	ciphertext := aead.Seal(nil, nonce, []byte(token), []byte(protocolContext+sessionID))
	return TokenEnvelope{
		EncryptedToken:  rawBase64.EncodeToString(ciphertext),
		ServerPublicKey: rawBase64.EncodeToString(serverPrivate.PublicKey().Bytes()),
		Nonce:           rawBase64.EncodeToString(nonce),
		KDFSalt:         rawBase64.EncodeToString(salt),
		ProtocolVersion: EnvelopeVersion,
	}
}

func bytesOf(length int, value byte) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = value
	}
	return result
}
