package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateConfigs(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(10808, 10809, 10800, 8, nil)

	// Test client config generation
	cfgPath, err := mgr.GenerateConfig(8, "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatalf("failed to generate client config: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read generated client config: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("generated config is not valid JSON: %v", err)
	}

	// Test server config generation
	srvMgr := NewServerManager(10001, nil)
	srvCfgPath, err := srvMgr.GenerateServerConfig("11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatalf("failed to generate server config: %v", err)
	}

	srvData, err := os.ReadFile(srvCfgPath)
	if err != nil {
		t.Fatalf("failed to read generated server config: %v", err)
	}

	var srvParsed map[string]any
	if err := json.Unmarshal(srvData, &srvParsed); err != nil {
		t.Fatalf("generated server config is not valid JSON: %v", err)
	}

	_ = tempDir
	_ = filepath.Dir(cfgPath)
}
