package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type secretCipher struct{ aead cipher.AEAD }

// ConfigureSecrets loads the local key used to encrypt target passwords in the database.
// Back up this file together with the database; losing it makes saved passwords unreadable.
func (s *Store) ConfigureSecrets(dataDir string) error {
	path := filepath.Join(dataDir, "target-password.key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		var savedPasswords int
		if countErr := s.db.QueryRow(`SELECT COUNT(*) FROM targets WHERE password_ciphertext IS NOT NULL AND password_ciphertext <> ''`).Scan(&savedPasswords); countErr != nil {
			return countErr
		}
		if savedPasswords != 0 {
			return errors.New("target password key is missing; restore target-password.key from backup")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(createErr, os.ErrExist) {
			key, err = os.ReadFile(path)
		} else if createErr != nil {
			return fmt.Errorf("create target password key: %w", createErr)
		} else {
			_, err = file.Write(key)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return fmt.Errorf("read target password key: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("target password key permissions must be 0600")
	}
	if len(key) != 32 {
		return errors.New("target password key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	s.secrets = &secretCipher{aead: aead}
	return nil
}

func (c *secretCipher) encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *secretCipher) decrypt(encoded string) (string, error) {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if len(sealed) < c.aead.NonceSize() {
		return "", errors.New("invalid encrypted password")
	}
	plaintext, err := c.aead.Open(nil, sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
