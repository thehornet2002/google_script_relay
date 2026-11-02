package relay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"
)

type PingResult struct {
	Count         int     `json:"count"`
	Received      int     `json:"received"`
	MinMs         float64 `json:"min_ms"`
	MaxMs         float64 `json:"max_ms"`
	AvgMs         float64 `json:"avg_ms"`
	MedianMs      float64 `json:"median_ms"`
	TrimmedMeanMs float64 `json:"trimmed_mean_ms"` // Outlier-rejected mean
	JitterMs      float64 `json:"jitter_ms"`
	LossRate      float64 `json:"loss_rate"`
	PureNetworkMs float64 `json:"pure_network_ms"` // Deducts server internal processing time
}

type SpeedResult struct {
	TotalBytes   int           `json:"total_bytes"`
	Duration     time.Duration `json:"duration"`
	SpeedMBps    float64       `json:"speed_mbps"`
	SpeedMbpsNet float64       `json:"speed_mbps_net"`
	PeakSpeedMB  float64       `json:"peak_speed_mb"`
	StabilityPct float64       `json:"stability_pct"` // Throughput consistency index 0-100%
	CRCVerified  bool          `json:"crc_verified"`
}

// RunPingBenchmark sends a warm-up packet followed by high-precision probes directly to the VPS
func RunPingBenchmark(client *http.Client, pool *EndpointPool, token string, count int) (PingResult, error) {
	if count <= 0 {
		count = 4
	}

	// 1. Warm-up probe (discards cold-start/renegotiation penalty)
	warmupPkt := Packet{
		SessionID: "__ping__",
		Token:     token,
	}
	_, _, _ = pool.SendPacket(client, warmupPkt, nil)
	time.Sleep(50 * time.Millisecond)

	// 2. High-precision timed measurement probes
	totalProbes := count
	if totalProbes < 4 {
		totalProbes = 4
	}

	rtts := make([]float64, 0, totalProbes)
	netRtts := make([]float64, 0, totalProbes)

	for i := 0; i < totalProbes; i++ {
		start := time.Now()
		pkt := Packet{
			SessionID: "__ping__",
			Token:     token,
		}

		resp, _, err := pool.SendPacket(client, pkt, nil)
		rtt := float64(time.Since(start).Microseconds()) / 1000.0

		if err == nil && resp != nil {
			rtts = append(rtts, rtt)

			// If server provided nano processing timestamps, compute pure network transit time
			if resp.ServerTxNano > resp.ServerRxNano && resp.ServerRxNano > 0 {
				serverProcMs := float64(resp.ServerTxNano-resp.ServerRxNano) / 1e6
				netRtt := rtt - serverProcMs
				if netRtt > 0 {
					netRtts = append(netRtts, netRtt)
				} else {
					netRtts = append(netRtts, rtt)
				}
			} else {
				netRtts = append(netRtts, rtt)
			}
		}

		if i < totalProbes-1 {
			time.Sleep(60 * time.Millisecond)
		}
	}

	res := PingResult{
		Count:    totalProbes,
		Received: len(rtts),
	}

	if len(rtts) == 0 {
		res.LossRate = 1.0
		return res, fmt.Errorf("all ping packets dropped")
	}

	res.LossRate = float64(totalProbes-len(rtts)) / float64(totalProbes)

	// Sort for percentile and outlier calculations
	sortedRTTs := make([]float64, len(rtts))
	copy(sortedRTTs, rtts)
	sort.Float64s(sortedRTTs)

	res.MinMs = sortedRTTs[0]
	res.MaxMs = sortedRTTs[len(sortedRTTs)-1]
	res.MedianMs = sortedRTTs[len(sortedRTTs)/2]

	var sum float64
	for _, v := range rtts {
		sum += v
	}
	res.AvgMs = sum / float64(len(rtts))

	// Trimmed Mean (removes the single highest outlier if >= 3 samples)
	if len(sortedRTTs) >= 3 {
		var trimmedSum float64
		for i := 0; i < len(sortedRTTs)-1; i++ {
			trimmedSum += sortedRTTs[i]
		}
		res.TrimmedMeanMs = trimmedSum / float64(len(sortedRTTs)-1)
	} else {
		res.TrimmedMeanMs = res.AvgMs
	}

	// Calculate True Jitter (Standard Deviation of RTTs)
	var varSum float64
	for _, v := range rtts {
		diff := v - res.AvgMs
		varSum += diff * diff
	}
	res.JitterMs = math.Sqrt(varSum / float64(len(rtts)))

	// Pure Network Latency average
	if len(netRtts) > 0 {
		var netSum float64
		for _, v := range netRtts {
			netSum += v
		}
		res.PureNetworkMs = netSum / float64(len(netRtts))
	} else {
		res.PureNetworkMs = res.AvgMs
	}

	return res, nil
}

// RunSpeedBenchmark downloads exactly 4 MB of data directly from the VPS server through the tunnel
// with CRC32 chunk verification and instantaneous throughput tracking.
func RunSpeedBenchmark(client *http.Client, pool *EndpointPool, token string, onProgress func(done, total int, speedMBps float64)) (SpeedResult, error) {
	offset := 0
	totalSize := BenchmarkFileSize
	startTime := time.Now()

	var peakSpeed float64
	var chunkSpeeds []float64
	crcVerified := true

	for offset < totalSize {
		chunkStart := time.Now()
		pkt := Packet{
			SessionID: "__bench_4mb__",
			Token:     token,
			Data:      base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset))),
		}

		resp, _, err := pool.SendPacket(client, pkt, nil)
		if err != nil {
			return SpeedResult{}, fmt.Errorf("speedtest chunk failed at offset %d: %w", offset, err)
		}

		if resp.TotalSize > 0 {
			totalSize = resp.TotalSize
		}

		chunkDur := time.Since(chunkStart).Seconds()
		if resp.Data != "" {
			decoded, err := base64.StdEncoding.DecodeString(resp.Data)
			if err != nil {
				return SpeedResult{}, fmt.Errorf("invalid chunk data: %w", err)
			}

			// Validate chunk integrity with CRC32
			if resp.CRC32 != 0 {
				actualCRC := crc32.ChecksumIEEE(decoded)
				if actualCRC != resp.CRC32 {
					crcVerified = false
				}
			}

			chunkBytes := len(decoded)
			offset += chunkBytes

			if chunkDur > 0.001 {
				instantSpeed := (float64(chunkBytes) / (1024 * 1024)) / chunkDur
				chunkSpeeds = append(chunkSpeeds, instantSpeed)
				if instantSpeed > peakSpeed {
					peakSpeed = instantSpeed
				}
			}
		} else if resp.Offset > offset {
			offset = resp.Offset
		} else {
			break
		}

		elapsed := time.Since(startTime).Seconds()
		var currentAvgSpeed float64
		if elapsed > 0.05 {
			currentAvgSpeed = (float64(offset) / (1024 * 1024)) / elapsed
		}
		if onProgress != nil {
			onProgress(offset, totalSize, currentAvgSpeed)
		}

		if resp.Close || offset >= totalSize {
			break
		}
	}

	duration := time.Since(startTime)
	if duration <= 0 {
		duration = time.Millisecond
	}

	mbTransferred := float64(offset) / (1024 * 1024)
	speedMBps := mbTransferred / duration.Seconds()
	speedMbpsNet := speedMBps * 8.0

	// Calculate stability index based on variance of chunk speeds
	stability := 100.0
	if len(chunkSpeeds) > 1 && speedMBps > 0 {
		var diffSum float64
		for _, s := range chunkSpeeds {
			diffSum += math.Abs(s - speedMBps)
		}
		avgDev := diffSum / float64(len(chunkSpeeds))
		cv := avgDev / speedMBps // Coefficient of variation
		stability = math.Max(0.0, math.Min(100.0, (1.0-cv)*100.0))
	}

	return SpeedResult{
		TotalBytes:   offset,
		Duration:     duration,
		SpeedMBps:    speedMBps,
		SpeedMbpsNet: speedMbpsNet,
		PeakSpeedMB:  peakSpeed,
		StabilityPct: stability,
		CRCVerified:  crcVerified,
	}, nil
}

// SendAdminCommand sends a management command to the VPS over the Google Script tunnel
func SendAdminCommand(client *http.Client, pool *EndpointPool, token string, req AdminRequest) (*AdminResponse, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	pkt := Packet{
		SessionID: "__admin__",
		Token:     token,
		Data:      base64.StdEncoding.EncodeToString(raw),
	}

	resp, _, err := pool.SendPacket(client, pkt, nil)
	if err != nil {
		return nil, fmt.Errorf("admin request failed: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("admin error from server: %s", resp.Error)
	}

	decoded, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("invalid admin response encoding: %w", err)
	}

	var res AdminResponse
	if err := json.Unmarshal(decoded, &res); err != nil {
		return nil, fmt.Errorf("invalid admin response json: %w", err)
	}

	return &res, nil
}
