package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

type Crypto struct {
	gcm     cipher.AEAD
	hmacKey []byte
}

func New(aesKey, hmacKey []byte) (*Crypto, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("crypto.New: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto.New: %w", err)
	}

	return &Crypto{gcm: gcm, hmacKey: hmacKey}, nil
}

func (c *Crypto) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto.Encrypt: %w", err)
	}

	return c.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *Crypto) Decrypt(ciphertext []byte) ([]byte, error) {
	ns := c.gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, fmt.Errorf("crypto.Decrypt: ciphertext too short")
	}

	return c.gcm.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
}

func (c *Crypto) Hash(data []byte) []byte {
	m := hmac.New(sha256.New, c.hmacKey)
	m.Write(data)
	return m.Sum(nil)
}
