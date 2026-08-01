package coreserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const maximumLoginSealedPlaintextBytes = 12 * 1024

type LoginTransactionSealer struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewLoginTransactionSealer(key []byte) (*LoginTransactionSealer, error) {
	if len(key) != 32 {
		return nil, errors.New("login transaction sealing key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize login transaction AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize login transaction GCM: %w", err)
	}
	return &LoginTransactionSealer{aead: aead, random: rand.Reader}, nil
}

func (sealer *LoginTransactionSealer) Seal(scope, purpose string, plaintext []byte) ([]byte, error) {
	if sealer == nil || sealer.aead == nil || sealer.random == nil {
		return nil, errors.New("login transaction sealer is not initialized")
	}
	if err := validateSealScope(scope, purpose); err != nil {
		return nil, err
	}
	if len(plaintext) < 1 || len(plaintext) > maximumLoginSealedPlaintextBytes {
		return nil, errors.New("login transaction plaintext is outside sealing bounds")
	}
	nonce := make([]byte, sealer.aead.NonceSize())
	if _, err := io.ReadFull(sealer.random, nonce); err != nil {
		return nil, fmt.Errorf("generate login transaction sealing nonce: %w", err)
	}
	result := append([]byte(nil), nonce...)
	result = sealer.aead.Seal(result, nonce, plaintext, loginSealAAD(scope, purpose))
	return result, nil
}

func (sealer *LoginTransactionSealer) Unseal(scope, purpose string, sealed []byte) ([]byte, error) {
	if sealer == nil || sealer.aead == nil {
		return nil, errors.New("login transaction sealer is not initialized")
	}
	if err := validateSealScope(scope, purpose); err != nil {
		return nil, err
	}
	nonceSize := sealer.aead.NonceSize()
	if len(sealed) < nonceSize+sealer.aead.Overhead()+1 || len(sealed) > nonceSize+sealer.aead.Overhead()+maximumLoginSealedPlaintextBytes {
		return nil, errors.New("sealed login transaction is outside protocol bounds")
	}
	plaintext, err := sealer.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], loginSealAAD(scope, purpose))
	if err != nil {
		return nil, errors.New("sealed login transaction failed authentication")
	}
	return plaintext, nil
}

func validateSealScope(scope, purpose string) error {
	if scope == "" || len(scope) > 256 || purpose == "" || len(purpose) > 64 {
		return errors.New("login transaction sealing scope or purpose is invalid")
	}
	return nil
}

func loginSealAAD(scope, purpose string) []byte {
	return []byte("agentserver-v2/login-bridge/aes-256-gcm/v1\x00" + purpose + "\x00" + scope)
}
