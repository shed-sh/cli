package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const protocolContext = "shed-cli-login-v1:"

var rawBase64 = base64.RawURLEncoding

type Encryption struct {
	privateKey *ecdh.PrivateKey
}

func NewEncryption() (*Encryption, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate login encryption key: %w", err)
	}
	return &Encryption{privateKey: privateKey}, nil
}

func (e *Encryption) PublicKey() string {
	return rawBase64.EncodeToString(e.privateKey.PublicKey().Bytes())
}

func (e *Encryption) Decrypt(sessionID string, envelope TokenEnvelope) (string, error) {
	if envelope.ProtocolVersion != EnvelopeVersion {
		return "", fmt.Errorf("unsupported login protocol version %q", envelope.ProtocolVersion)
	}
	if sessionID == "" {
		return "", errors.New("login session ID is empty")
	}

	publicKeyBytes, err := decodeExact("server public key", envelope.ServerPublicKey, 32)
	if err != nil {
		return "", err
	}
	serverPublicKey, err := ecdh.X25519().NewPublicKey(publicKeyBytes)
	if err != nil {
		return "", fmt.Errorf("decode server public key: %w", err)
	}
	sharedSecret, err := e.privateKey.ECDH(serverPublicKey)
	if err != nil {
		return "", fmt.Errorf("derive login shared secret: %w", err)
	}

	salt, err := decodeExact("KDF salt", envelope.KDFSalt, 32)
	if err != nil {
		return "", err
	}
	key := make([]byte, 32)
	derivation := hkdf.New(sha256.New, sharedSecret, salt, []byte(protocolContext+sessionID))
	if _, err := io.ReadFull(derivation, key); err != nil {
		return "", fmt.Errorf("derive login encryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create login cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create login cipher mode: %w", err)
	}
	nonce, err := decodeExact("nonce", envelope.Nonce, aead.NonceSize())
	if err != nil {
		return "", err
	}
	ciphertext, err := rawBase64.DecodeString(envelope.EncryptedToken)
	if err != nil {
		return "", fmt.Errorf("decode encrypted token: %w", err)
	}
	if len(ciphertext) < aead.Overhead() {
		return "", errors.New("encrypted token is too short")
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(protocolContext+sessionID))
	if err != nil {
		return "", fmt.Errorf("decrypt access token: %w", err)
	}
	token := string(plaintext)
	if err := validateToken(token); err != nil {
		return "", err
	}
	return token, nil
}

func decodeExact(name, value string, expected int) ([]byte, error) {
	decoded, err := rawBase64.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if len(decoded) != expected {
		return nil, fmt.Errorf("%s has length %d, want %d", name, len(decoded), expected)
	}
	return decoded, nil
}

func validateToken(token string) error {
	const prefix = "shed_pat_"
	if !strings.HasPrefix(token, prefix) {
		return errors.New("decrypted credential is not a Shed token")
	}
	secret, err := rawBase64.DecodeString(strings.TrimPrefix(token, prefix))
	if err != nil || len(secret) != 32 {
		return errors.New("decrypted Shed token is malformed")
	}
	return nil
}
