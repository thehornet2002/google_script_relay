package relay

import (
	"testing"
)

func TestConfigDefaultsAndValidation(t *testing.T) {
	cfg := Config{
		Mode:  "client",
		Token: "my-super-secret-token",
		GasURLs: []string{
			"https://script.google.com/macros/s/ABC/exec",
			"https://script.google.com/macros/s/ABC/exec", // Duplicate to test deduplication
			"https://script.google.com/macros/s/DEF/exec",
		},
	}

	cfg.EnsureDefaults()

	if cfg.SocksPort != 10808 {
		t.Errorf("expected default SocksPort 10808, got %d", cfg.SocksPort)
	}
	if cfg.HttpPort != 10809 {
		t.Errorf("expected default HttpPort 10809, got %d", cfg.HttpPort)
	}
	if cfg.XrayUUID == "" {
		t.Errorf("expected generated XrayUUID")
	}
	if cfg.AdminToken != cfg.Token {
		t.Errorf("expected AdminToken to fallback to Token")
	}

	deduped := cfg.GetGasURLs()
	if len(deduped) != 2 {
		t.Errorf("expected 2 unique URLs, got %d", len(deduped))
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config failed validation: %v", err)
	}

	// Test invalid: empty token
	invalidCfg := cfg
	invalidCfg.Token = ""
	if err := invalidCfg.Validate(); err == nil {
		t.Errorf("expected error on empty token")
	}

	// Test invalid: short token
	invalidCfg.Token = "short"
	if err := invalidCfg.Validate(); err == nil {
		t.Errorf("expected error on short token")
	}

	// Test invalid: client without gas URLs
	invalidClient := cfg
	invalidClient.GasURL = ""
	invalidClient.GasURLs = nil
	if err := invalidClient.Validate(); err == nil {
		t.Errorf("expected error on client with no gas URLs")
	}
}
