// Package vault encrypts provider credentials with AES-256-GCM.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

type Vault struct {
	aead cipher.AEAD
}

func New(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, errors.New("vault key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// Seal encrypts plaintext under a fresh random nonce. aad binds the ciphertext
// to its owner (the credential name) so it can't be swapped between rows.
func (v *Vault) Seal(aad string, plaintext []byte) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, v.aead.Seal(nil, nonce, plaintext, []byte(aad)), nil
}

func (v *Vault) Open(aad string, nonce, ciphertext []byte) ([]byte, error) {
	return v.aead.Open(nil, nonce, ciphertext, []byte(aad))
}
