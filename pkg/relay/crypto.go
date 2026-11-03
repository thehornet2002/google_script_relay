package relay

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateSecureToken returns a cryptographically secure URL-safe token.
func GenerateSecureToken(byteLen int) (string, error) {
	if byteLen < 16 {
		byteLen = 32
	}
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to read secure random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateUUID generates a random RFC 4122 v4 UUID string.
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ComputeSessionBindingHMAC generates a deterministic HMAC signature for a session ID using the token.
func ComputeSessionBindingHMAC(token, sessionID string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// ConstantTimeCompare compares two strings in constant time to mitigate timing side-channel attacks.
func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
