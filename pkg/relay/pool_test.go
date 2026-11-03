package relay

import (
	"testing"
	"time"
)

func TestEndpointPoolSelection(t *testing.T) {
	urls := []string{
		"https://script.google.com/macros/s/EP1/exec",
		"https://script.google.com/macros/s/EP2/exec",
		"https://script.google.com/macros/s/EP3/exec",
	}

	pool, err := NewEndpointPool(urls, nil)
	if err != nil {
		t.Fatalf("failed to create endpoint pool: %v", err)
	}

	// Initial pick
	ep1 := pool.PickEndpoint()
	if ep1 == nil {
		t.Fatalf("expected valid endpoint, got nil")
	}

	// Simulate high load on ep1
	ep1.ActiveInFlight.Store(10)

	// Next pick should pick a different less loaded endpoint
	ep2 := pool.PickEndpoint()
	if ep2.ID == ep1.ID {
		t.Fatalf("pool should pick endpoint with lower in-flight load")
	}

	// Simulate cooldown on all endpoints except EP3
	pool.endpoints[0].DisabledUntil.Store(time.Now().Add(10 * time.Minute).Unix())
	pool.endpoints[1].DisabledUntil.Store(time.Now().Add(10 * time.Minute).Unix())

	epAvailable := pool.PickEndpoint()
	if epAvailable.ID != 3 {
		t.Fatalf("pool should select available non-cooldown endpoint EP3, got ID %d", epAvailable.ID)
	}
}
