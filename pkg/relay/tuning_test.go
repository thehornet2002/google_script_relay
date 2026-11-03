package relay

import (
	"testing"
)

func TestClientTuningValidation(t *testing.T) {
	def := DefaultClientTuning()
	if err := def.Validate(); err != nil {
		t.Fatalf("default client tuning failed validation: %v", err)
	}

	invalidWorkers := def
	invalidWorkers.Workers = 0
	if err := invalidWorkers.Validate(); err == nil {
		t.Errorf("expected error for Workers=0")
	}

	invalidBuffer := def
	invalidBuffer.SocketBufferKB = 8
	if err := invalidBuffer.Validate(); err == nil {
		t.Errorf("expected error for SocketBufferKB=8 (minimum 16)")
	}

	invalidIdle := def
	invalidIdle.DownlinkMaxIdleMs = 50
	if err := invalidIdle.Validate(); err == nil {
		t.Errorf("expected error for DownlinkMaxIdleMs=50 (minimum 100)")
	}
}

func TestServerTuningValidation(t *testing.T) {
	def := DefaultServerTuning()
	if err := def.Validate(); err != nil {
		t.Fatalf("default server tuning failed validation: %v", err)
	}

	invalidWait := def
	invalidWait.WaitTimeoutMs = 50
	if err := invalidWait.Validate(); err == nil {
		t.Errorf("expected error for WaitTimeoutMs=50 (minimum 100)")
	}

	invalidDrain := def
	invalidDrain.MaxDrainBatchBytes = 1000
	if err := invalidDrain.Validate(); err == nil {
		t.Errorf("expected error for MaxDrainBatchBytes=1000 (minimum 8192)")
	}
}
