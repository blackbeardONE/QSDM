package account

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

func randomToken(bytes int) (string, error) {
	if bytes < 16 {
		return "", errors.New("random token size is too small")
	}
	raw := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func keyedHash(key []byte, kind, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func encryptString(key []byte, context, value string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(value), []byte(context))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func decryptString(key []byte, context, value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", errors.New("decode encrypted identity")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("encrypted identity is truncated")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(context))
	if err != nil {
		return "", errors.New("decrypt identity")
	}
	return string(plain), nil
}

func normalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if len(email) < 3 || len(email) > 254 || strings.Count(email, "@") != 1 {
		return "", errors.New("enter a valid email address")
	}
	parts := strings.SplitN(email, "@", 2)
	if parts[0] == "" || parts[1] == "" || !strings.Contains(parts[1], ".") {
		return "", errors.New("enter a valid email address")
	}
	for _, r := range email {
		if r <= 0x20 || r >= 0x7f {
			return "", errors.New("email addresses must use printable ASCII characters")
		}
	}
	return email, nil
}

func maskEmail(value string) string {
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return ""
	}
	local := parts[0][:1]
	if len(parts[0]) > 2 {
		local += strings.Repeat("*", len(parts[0])-2) + parts[0][len(parts[0])-1:]
	} else if len(parts[0]) == 2 {
		local += "*"
	}
	return local + "@" + parts[1]
}
