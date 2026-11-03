package relay

import (
	"testing"
)

func TestGenerateSecureToken(t *testing.T) {
	tok1, err := GenerateSecureToken(32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tok1) < 32 {
		t.Fatalf("token too short: %s", tok1)
	}

	tok2, _ := GenerateSecureToken(32)
	if tok1 == tok2 {
		t.Fatalf("tokens must be unique: %s == %s", tok1, tok2)
	}
}

func TestGenerateUUID(t *testing.T) {
	u := GenerateUUID()
	if len(u) != 36 {
		t.Fatalf("invalid UUID length: %s", u)
	}
	if u[14] != '4' {
		t.Fatalf("UUID version must be 4: %s", u)
	}
}

func TestSessionBindingHMAC(t *testing.T) {
	token := "test-secret-token-12345"
	sessID := "abcdef123456"

	sig1 := ComputeSessionBindingHMAC(token, sessID)
	sig2 := ComputeSessionBindingHMAC(token, sessID)
	if sig1 != sig2 {
		t.Fatalf("HMAC signatures must be deterministic: %s != %s", sig1, sig2)
	}

	sigDiff := ComputeSessionBindingHMAC(token, "different-session")
	if sig1 == sigDiff {
		t.Fatalf("HMAC signatures must differ for different session IDs")
	}

	if !ConstantTimeCompare(sig1, sig2) {
		t.Fatalf("ConstantTimeCompare should return true for identical strings")
	}
	if ConstantTimeCompare(sig1, sigDiff) {
		t.Fatalf("ConstantTimeCompare should return false for different strings")
	}
}
