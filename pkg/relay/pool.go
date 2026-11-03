package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Endpoint represents a single Google Apps Script deployment URL with health stats.
type Endpoint struct {
	ID                int
	URL               string
	ActiveInFlight    atomic.Int64
	TotalRequests     atomic.Int64
	TotalErrors       atomic.Int64
	ConsecutiveErrors atomic.Int32
	AvgLatencyMs      atomic.Int64 // Exponential moving average of latency in ms
	DisabledUntil     atomic.Int64 // Unix timestamp in seconds
	LastError         string
	lastErrMu         sync.RWMutex
}

func (e *Endpoint) IsAvailable(now int64) bool {
	return now >= e.DisabledUntil.Load()
}

func (e *Endpoint) ShortName() string {
	parts := strings.Split(e.URL, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part != "" && part != "exec" && len(part) > 10 {
			if len(part) > 18 {
				return fmt.Sprintf("GAS#%d(%s...%s)", e.ID, part[:6], part[len(part)-4:])
			}
			return fmt.Sprintf("GAS#%d(%s)", e.ID, part)
		}
	}
	return fmt.Sprintf("GAS#%d", e.ID)
}

func (e *Endpoint) GetLastError() string {
	e.lastErrMu.RLock()
	defer e.lastErrMu.RUnlock()
	return e.LastError
}

func (e *Endpoint) SetLastError(errStr string) {
	e.lastErrMu.Lock()
	e.LastError = errStr
	e.lastErrMu.Unlock()
}

func (e *Endpoint) UpdateLatency(latencyMs int64) {
	old := e.AvgLatencyMs.Load()
	if old == 0 {
		e.AvgLatencyMs.Store(latencyMs)
	} else {
		// 80% old, 20% new
		e.AvgLatencyMs.Store((old*4 + latencyMs) / 5)
	}
}

// EndpointPool manages multiple Google Apps Script accounts with intelligent load balancing.
type EndpointPool struct {
	endpoints []*Endpoint
	LogFunc   func(format string, args ...any)
}

func NewEndpointPool(urls []string, logFunc func(format string, args ...any)) (*EndpointPool, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("no Google Apps Script URLs provided")
	}

	endpoints := make([]*Endpoint, len(urls))
	for i, u := range urls {
		endpoints[i] = &Endpoint{
			ID:  i + 1,
			URL: u,
		}
	}

	return &EndpointPool{
		endpoints: endpoints,
		LogFunc:   logFunc,
	}, nil
}

func (p *EndpointPool) log(format string, args ...any) {
	if p.LogFunc != nil {
		p.LogFunc(format, args...)
	}
}

// PickEndpoint selects the healthiest endpoint with the lowest in-flight load and best latency.
func (p *EndpointPool) PickEndpoint() *Endpoint {
	if len(p.endpoints) == 1 {
		return p.endpoints[0]
	}

	now := time.Now().Unix()
	var best *Endpoint
	var bestScore int64 = 1<<62 - 1

	// 1. Pick among available (non-cooldown) endpoints
	for _, ep := range p.endpoints {
		if ep.IsAvailable(now) {
			inFlight := ep.ActiveInFlight.Load()
			lat := ep.AvgLatencyMs.Load()
			// Score formula: heavily weight active in-flight requests, lightly weight latency
			score := (inFlight * 1000) + lat
			if score < bestScore {
				bestScore = score
				best = ep
			}
		}
	}

	// 2. If all endpoints are temporarily in cooldown, pick the one that expires soonest
	if best == nil {
		var earliestExpiry int64 = 1<<62 - 1
		for _, ep := range p.endpoints {
			exp := ep.DisabledUntil.Load()
			if exp < earliestExpiry {
				earliestExpiry = exp
				best = ep
			}
		}
	}

	return best
}

// SendPacket sends a packet to Google Apps Script. If the chosen endpoint fails or hits quota,
// it automatically fails over to another healthy endpoint in the pool.
func (p *EndpointPool) SendPacket(client *http.Client, packet Packet, preferred *Endpoint) (*ResponsePacket, *Endpoint, error) {
	// Ensure RequestID is present on data packets for server deduplication
	if packet.Data != "" && packet.RequestID == 0 {
		packet.RequestID = uint64(time.Now().UnixNano())
	}

	now := time.Now().Unix()
	target := preferred
	if target == nil || !target.IsAvailable(now) {
		target = p.PickEndpoint()
	}

	maxAttempts := len(p.endpoints)
	if maxAttempts > 4 {
		maxAttempts = 4
	}

	var lastErr error
	tried := make(map[int]bool)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		tried[target.ID] = true
		target.ActiveInFlight.Add(1)
		target.TotalRequests.Add(1)

		start := time.Now()
		resp, err := p.executeRequest(client, target.URL, packet)
		durMs := time.Since(start).Milliseconds()
		target.ActiveInFlight.Add(-1)

		if err == nil {
			target.ConsecutiveErrors.Store(0)
			target.UpdateLatency(durMs)
			return resp, target, nil
		}

		// Handle error & cooldown
		lastErr = err
		target.TotalErrors.Add(1)
		consec := target.ConsecutiveErrors.Add(1)
		errStr := err.Error()
		target.SetLastError(errStr)

		is403 := strings.Contains(errStr, "403") || strings.Contains(errStr, "دسترسی رد شد") || strings.Contains(errStr, "به دسترسی نیاز دارید")
		isQuota := strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "Service invoked too many times") ||
			strings.Contains(errStr, "Rate Limit Exceeded") ||
			strings.Contains(errStr, "limit reached")

		if is403 {
			cooldown := 10 * time.Minute
			target.DisabledUntil.Store(time.Now().Add(cooldown).Unix())
			p.log("[Pool] %s returned 403 Forbidden! Check 'Who has access: Anyone' in Google Script. Cooldown %v", target.ShortName(), cooldown)
		} else if isQuota {
			cooldown := 3 * time.Minute
			target.DisabledUntil.Store(time.Now().Add(cooldown).Unix())
			p.log("[Pool] %s quota exceeded! Cooldown %v. Error: %s", target.ShortName(), cooldown, errStr)
		} else if consec >= 3 {
			cooldown := 30 * time.Second
			target.DisabledUntil.Store(time.Now().Add(cooldown).Unix())
			p.log("[Pool] %s had 3 consecutive errors! Cooldown %v. Error: %s", target.ShortName(), cooldown, errStr)
		}

		// Pick next available endpoint for failover
		var next *Endpoint
		var lowestScore int64 = 1<<62 - 1
		for _, ep := range p.endpoints {
			if !tried[ep.ID] && ep.IsAvailable(time.Now().Unix()) {
				score := (ep.ActiveInFlight.Load() * 1000) + ep.AvgLatencyMs.Load()
				if score < lowestScore {
					lowestScore = score
					next = ep
				}
			}
		}

		if next == nil {
			break
		}
		p.log("[Pool] Session %s failover %s -> %s", packet.SessionID, target.ShortName(), next.ShortName())
		target = next
	}

	return nil, target, lastErr
}

func (p *EndpointPool) executeRequest(client *http.Client, urlStr string, packet Packet) (*ResponsePacket, error) {
	data, err := json.Marshal(packet)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", urlStr, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("HTTP 403 Forbidden: deployment access not set to Anyone")
	}
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("HTTP 429 Too Many Requests: %s", string(body))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var resPacket ResponsePacket
	if err := json.Unmarshal(body, &resPacket); err != nil {
		bodyStr := string(body)
		if strings.Contains(bodyStr, "Service invoked too many times") {
			return nil, fmt.Errorf("Google Apps Script quota exceeded: %s", bodyStr)
		}
		if strings.Contains(bodyStr, "به دسترسی نیاز دارید") || strings.Contains(bodyStr, "دسترسی رد شد") {
			return nil, fmt.Errorf("HTTP 403: Google Script access denied")
		}
		return nil, fmt.Errorf("invalid response json (status %d): %s", resp.StatusCode, bodyStr)
	}

	if resPacket.Error != "" {
		if strings.Contains(resPacket.Error, "Service invoked too many times") {
			return nil, fmt.Errorf("Google Script error: %s", resPacket.Error)
		}
	}

	return &resPacket, nil
}

// GetStatusSummary returns a formatted overview of all endpoints and their health.
func (p *EndpointPool) GetStatusSummary() string {
	now := time.Now().Unix()
	var b strings.Builder
	for _, ep := range p.endpoints {
		avail := ep.IsAvailable(now)
		status := "Active"
		if !avail {
			remaining := ep.DisabledUntil.Load() - now
			status = fmt.Sprintf("Cooldown(%ds)", remaining)
		}
		b.WriteString(fmt.Sprintf("[%s: %s | Reqs: %d, Active: %d, Lat: %dms] ",
			ep.ShortName(), status, ep.TotalRequests.Load(), ep.ActiveInFlight.Load(), ep.AvgLatencyMs.Load()))
	}
	return b.String()
}
