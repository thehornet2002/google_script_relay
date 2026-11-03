package relay

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync/atomic"
	"time"
)

type ProfileType int

const (
	ProfilePing ProfileType = iota + 1
	ProfileSpeed
	ProfileBalanced
)

func (p ProfileType) String() string {
	switch p {
	case ProfilePing:
		return "Best Ping"
	case ProfileSpeed:
		return "Best Download Speed"
	case ProfileBalanced:
		return "Balanced (Ping & Speed)"
	default:
		return "Balanced"
	}
}

type MatrixCandidate struct {
	Name         string
	ClientTuning ClientTuning
	ServerTuning ServerTuning
}

type StepResult struct {
	Step        int
	TotalSteps  int
	Candidate   MatrixCandidate
	Ping        PingResult
	Speed       SpeedResult
	Score       float64
	IsBestSoFar bool
	CurrentBest float64
}

type OptimizationReport struct {
	Profile       ProfileType
	BestScore     float64
	BestCandidate MatrixCandidate
	BestClient    ClientTuning
	BestServer    ServerTuning
	BestPing      PingResult
	BestSpeed     SpeedResult
	TotalSteps    int
	Duration      time.Duration
	StepHistory   []StepResult
}

type Optimizer struct {
	client     *Client
	token      string
	adminToken string
	httpClient *http.Client
	pool       *EndpointPool
	cancelFlag atomic.Bool
	LogFunc    func(format string, args ...any)
}

func NewOptimizer(c *Client) *Optimizer {
	admTok := c.cfg.AdminToken
	if admTok == "" {
		admTok = c.cfg.Token
	}
	return &Optimizer{
		client:     c,
		token:      c.cfg.Token,
		adminToken: admTok,
		httpClient: c.httpClient,
		pool:       c.pool,
		LogFunc:    c.LogFunc,
	}
}

func (o *Optimizer) log(format string, args ...any) {
	if o.LogFunc != nil {
		o.LogFunc(format, args...)
	}
}

func (o *Optimizer) Cancel() {
	o.cancelFlag.Store(true)
}

// CalculateScore computes a normalized score based on network utility theory
func (o *Optimizer) CalculateScore(profile ProfileType, ping PingResult, speed SpeedResult) float64 {
	if ping.LossRate >= 0.5 || ping.MedianMs <= 0 {
		return 0.0
	}

	switch profile {
	case ProfilePing:
		// Target: Minimal Median latency, heavily penalize jitter and packet loss
		basePing := ping.MedianMs
		if basePing < 20 {
			basePing = 20
		}
		// Base score from inverse latency
		score := (1000.0 / basePing) * 10.0
		// Packet loss penalty
		score *= math.Max(0.1, 1.0-ping.LossRate*3.0)
		// Jitter penalty
		if ping.JitterMs > 0 {
			score /= (1.0 + (ping.JitterMs / 150.0))
		}
		return math.Max(0.01, score)

	case ProfileSpeed:
		// Target: Maximum sustained throughput factored by consistency and peak burst
		stabilityFactor := 0.7 + 0.3*(speed.StabilityPct/100.0)
		score := speed.SpeedMBps * stabilityFactor
		if speed.PeakSpeedMB > speed.SpeedMBps {
			score += (speed.PeakSpeedMB - speed.SpeedMBps) * 0.1
		}
		return score

	case ProfileBalanced:
		// Target: Delay-Bandwidth Product (BDP) saturation efficiency
		effectiveLat := ping.MedianMs + (ping.JitterMs * 0.6)
		if effectiveLat <= 0 {
			effectiveLat = 100
		}
		rawRatio := (speed.SpeedMBps * 1000.0) / effectiveLat
		stabilityFactor := 0.8 + 0.2*(speed.StabilityPct/100.0)
		lossFactor := math.Max(0.1, 1.0-ping.LossRate*2.0)
		return rawRatio * lossFactor * stabilityFactor

	default:
		return speed.SpeedMBps
	}
}

// GenerateDeepMatrix produces a comprehensive 8-candidate parameter matrix covering the full spectrum
func (o *Optimizer) GenerateDeepMatrix(profile ProfileType) []MatrixCandidate {
	switch profile {
	case ProfilePing:
		return []MatrixCandidate{
			{
				Name: "P1: Ultra-Fast Poll (2 Workers, Stagger 100ms, Hold 200ms, Drain 32KB, Mux Off)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 100, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 180, MuxConcurrency: 0, SocketBufferKB: 32, UplinkChunkKB: 16,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 200, MaxDrainBatchBytes: 32768, DownlinkQueueCap: 128},
			},
			{
				Name: "P2: Dual-Worker Low-Latency (2 Workers, Stagger 140ms, Hold 300ms, Drain 48KB, Mux Off)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 140, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 250, MuxConcurrency: 0, SocketBufferKB: 32, UplinkChunkKB: 16,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 300, MaxDrainBatchBytes: 49152, DownlinkQueueCap: 128},
			},
			{
				Name: "P3: Triple-Worker Staggered (3 Workers, Stagger 90ms, Hold 250ms, Drain 48KB, Mux Off)",
				ClientTuning: ClientTuning{
					Workers: 3, WorkerStaggerMs: 90, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 220, MuxConcurrency: 0, SocketBufferKB: 64, UplinkChunkKB: 16,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 250, MaxDrainBatchBytes: 49152, DownlinkQueueCap: 256},
			},
			{
				Name: "P4: Balanced Ping-Hold (2 Workers, Stagger 160ms, Hold 400ms, Drain 64KB, Mux 4)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 160, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 350, MuxConcurrency: 4, SocketBufferKB: 64, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 400, MaxDrainBatchBytes: 65536, DownlinkQueueCap: 256},
			},
			{
				Name: "P5: Single-Worker Zero-Interference (1 Worker, Hold 200ms, Drain 32KB, Mux Off)",
				ClientTuning: ClientTuning{
					Workers: 1, WorkerStaggerMs: 0, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 150, MuxConcurrency: 0, SocketBufferKB: 32, UplinkChunkKB: 16,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 200, MaxDrainBatchBytes: 32768, DownlinkQueueCap: 128},
			},
			{
				Name: "P6: Triple-Pipe Fast Drain (3 Workers, Stagger 120ms, Hold 350ms, Drain 64KB, Mux 4)",
				ClientTuning: ClientTuning{
					Workers: 3, WorkerStaggerMs: 120, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 280, MuxConcurrency: 4, SocketBufferKB: 64, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 350, MaxDrainBatchBytes: 65536, DownlinkQueueCap: 256},
			},
		}

	case ProfileSpeed:
		return []MatrixCandidate{
			{
				Name: "S1: Dual-Stream Balanced (2 Workers, Stagger 180ms, Hold 700ms, Drain 128KB, Mux 8)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 180, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 500, MuxConcurrency: 8, SocketBufferKB: 128, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 700, MaxDrainBatchBytes: 131072, DownlinkQueueCap: 256},
			},
			{
				Name: "S2: Dual-Stream Large Batch (2 Workers, Stagger 220ms, Hold 900ms, Drain 192KB, Mux 16)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 220, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 650, MuxConcurrency: 16, SocketBufferKB: 128, UplinkChunkKB: 64,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 900, MaxDrainBatchBytes: 196608, DownlinkQueueCap: 384},
			},
			{
				Name: "S3: Triple-Stream Pipe (3 Workers, Stagger 150ms, Hold 800ms, Drain 128KB, Mux 16)",
				ClientTuning: ClientTuning{
					Workers: 3, WorkerStaggerMs: 150, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 600, MuxConcurrency: 16, SocketBufferKB: 128, UplinkChunkKB: 64,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 800, MaxDrainBatchBytes: 131072, DownlinkQueueCap: 512},
			},
			{
				Name: "S4: Max Batch 256KB (2 Workers, Stagger 250ms, Hold 1100ms, Drain 256KB, Mux 16)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 250, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 800, MuxConcurrency: 16, SocketBufferKB: 128, UplinkChunkKB: 64,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 1100, MaxDrainBatchBytes: 262144, DownlinkQueueCap: 512},
			},
			{
				Name: "S5: Triple-Stream High Capacity (3 Workers, Stagger 180ms, Hold 1000ms, Drain 192KB, Mux 16)",
				ClientTuning: ClientTuning{
					Workers: 3, WorkerStaggerMs: 180, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 700, MuxConcurrency: 16, SocketBufferKB: 128, UplinkChunkKB: 64,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 1000, MaxDrainBatchBytes: 196608, DownlinkQueueCap: 512},
			},
			{
				Name: "S6: Quad-Worker Aggressive (4 Workers, Stagger 120ms, Hold 900ms, Drain 128KB, Mux 16)",
				ClientTuning: ClientTuning{
					Workers: 4, WorkerStaggerMs: 120, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 600, MuxConcurrency: 16, SocketBufferKB: 128, UplinkChunkKB: 64,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 900, MaxDrainBatchBytes: 131072, DownlinkQueueCap: 512},
			},
		}

	default: // ProfileBalanced
		return []MatrixCandidate{
			{
				Name: "B1: Optimal Equilibrium (2 Workers, Stagger 180ms, Hold 600ms, Drain 128KB, Mux 8)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 180, DownlinkFastPollMs: 5, DownlinkMaxIdleMs: 450, MuxConcurrency: 8, SocketBufferKB: 64, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 600, MaxDrainBatchBytes: 131072, DownlinkQueueCap: 256},
			},
			{
				Name: "B2: Responsive Low-Latency (2 Workers, Stagger 140ms, Hold 450ms, Drain 96KB, Mux 8)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 140, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 350, MuxConcurrency: 8, SocketBufferKB: 64, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 450, MaxDrainBatchBytes: 98304, DownlinkQueueCap: 256},
			},
			{
				Name: "B3: Higher Throughput Balanced (3 Workers, Stagger 160ms, Hold 750ms, Drain 160KB, Mux 8)",
				ClientTuning: ClientTuning{
					Workers: 3, WorkerStaggerMs: 160, DownlinkFastPollMs: 5, DownlinkMaxIdleMs: 550, MuxConcurrency: 8, SocketBufferKB: 128, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 750, MaxDrainBatchBytes: 163840, DownlinkQueueCap: 384},
			},
			{
				Name: "B4: Smooth Streaming (2 Workers, Stagger 160ms, Hold 550ms, Drain 128KB, Mux 4)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 160, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 400, MuxConcurrency: 4, SocketBufferKB: 64, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 550, MaxDrainBatchBytes: 131072, DownlinkQueueCap: 256},
			},
			{
				Name: "B5: Triple-Pipe Low Jitter (3 Workers, Stagger 130ms, Hold 500ms, Drain 96KB, Mux 8)",
				ClientTuning: ClientTuning{
					Workers: 3, WorkerStaggerMs: 130, DownlinkFastPollMs: 0, DownlinkMaxIdleMs: 380, MuxConcurrency: 8, SocketBufferKB: 64, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 500, MaxDrainBatchBytes: 98304, DownlinkQueueCap: 256},
			},
			{
				Name: "B6: Deep Buffer Stable (2 Workers, Stagger 200ms, Hold 800ms, Drain 160KB, Mux 8)",
				ClientTuning: ClientTuning{
					Workers: 2, WorkerStaggerMs: 200, DownlinkFastPollMs: 5, DownlinkMaxIdleMs: 600, MuxConcurrency: 8, SocketBufferKB: 128, UplinkChunkKB: 32,
				},
				ServerTuning: ServerTuning{WaitTimeoutMs: 800, MaxDrainBatchBytes: 163840, DownlinkQueueCap: 384},
			},
		}
	}
}

// GenerateFineTuningCandidates takes the winning candidate and creates precision variants around it
func (o *Optimizer) GenerateFineTuningCandidates(winner MatrixCandidate) []MatrixCandidate {
	c := winner.ClientTuning
	s := winner.ServerTuning

	return []MatrixCandidate{
		{
			Name: fmt.Sprintf("%s + FineTune-A (Stagger -20ms, Wait -50ms)", winner.Name),
			ClientTuning: ClientTuning{
				Workers: c.Workers, WorkerStaggerMs: int(math.Max(0, float64(c.WorkerStaggerMs-20))),
				DownlinkFastPollMs: c.DownlinkFastPollMs, DownlinkMaxIdleMs: c.DownlinkMaxIdleMs,
				MuxConcurrency: c.MuxConcurrency, SocketBufferKB: c.SocketBufferKB, UplinkChunkKB: c.UplinkChunkKB,
			},
			ServerTuning: ServerTuning{
				WaitTimeoutMs:      int(math.Max(150, float64(s.WaitTimeoutMs-50))),
				MaxDrainBatchBytes: s.MaxDrainBatchBytes, DownlinkQueueCap: s.DownlinkQueueCap,
			},
		},
		{
			Name: fmt.Sprintf("%s + FineTune-B (Stagger +30ms, Drain +32KB)", winner.Name),
			ClientTuning: ClientTuning{
				Workers: c.Workers, WorkerStaggerMs: c.WorkerStaggerMs + 30,
				DownlinkFastPollMs: c.DownlinkFastPollMs, DownlinkMaxIdleMs: c.DownlinkMaxIdleMs,
				MuxConcurrency: c.MuxConcurrency, SocketBufferKB: c.SocketBufferKB, UplinkChunkKB: c.UplinkChunkKB,
			},
			ServerTuning: ServerTuning{
				WaitTimeoutMs:      s.WaitTimeoutMs,
				MaxDrainBatchBytes: int(math.Min(262144, float64(s.MaxDrainBatchBytes+32768))),
				DownlinkQueueCap:   s.DownlinkQueueCap,
			},
		},
	}
}

// RunOptimization executes a comprehensive multi-tier matrix search
func (o *Optimizer) RunOptimization(ctx context.Context, profile ProfileType, onStep func(StepResult)) (*OptimizationReport, error) {
	o.cancelFlag.Store(false)
	tier1Candidates := o.GenerateDeepMatrix(profile)
	startTime := time.Now()

	o.log("═════════════════════════════════════════════════════════════════════")
	o.log("[Optimizer] STARTING DEEP TWO-STAGE OPTIMIZATION: %s", profile)
	o.log("[Optimizer] Phase 1: Evaluating %d architectural matrix candidates...", len(tier1Candidates))
	o.log("═════════════════════════════════════════════════════════════════════")

	var bestScore float64 = -1
	var bestCand MatrixCandidate
	var bestPing PingResult
	var bestSpeed SpeedResult
	var history []StepResult

	totalExpectedSteps := len(tier1Candidates) + 2 // Tier 1 + 2 fine-tuning steps

	evaluateCandidate := func(cand MatrixCandidate, stepIdx int) {
		stepNum := stepIdx + 1
		o.log("[Optimizer] ─── Step [%d/%d] ───", stepNum, totalExpectedSteps)
		o.log("[Optimizer] Evaluating: %s", cand.Name)

		// 1. Remotely apply candidate ServerTuning to the VPS
		adminReq := AdminRequest{
			Action: "set_tuning",
			Tuning: &cand.ServerTuning,
		}
		adminResp, err := SendAdminCommand(o.httpClient, o.pool, o.adminToken, adminReq)
		if err != nil || (adminResp != nil && !adminResp.Success) {
			errMsg := "unknown"
			if err != nil {
				errMsg = err.Error()
			} else if adminResp != nil {
				errMsg = adminResp.Message
			}
			o.log("[Optimizer] Warning: failed to apply server tuning remotely: %s. Candidate skipped.", errMsg)
			return
		}

		// Apply locally to client
		o.client.SetTuning(cand.ClientTuning)
		time.Sleep(250 * time.Millisecond)

		// 2. High-precision Ping Measurement (Warm-up probe + 6 timed probes)
		o.log("[Optimizer] [%d/%d] Probing VPS latency (Warmup + 6 timed probes)...", stepNum, totalExpectedSteps)
		pingRes, err := RunPingBenchmark(o.httpClient, o.pool, o.token, 6)
		if err != nil {
			o.log("[Optimizer] Warning: ping probe issue: %v", err)
		} else {
			o.log("[Optimizer] [%d/%d] Ping Metrics: Median=%.1fms | TrimmedAvg=%.1fms | PureNet=%.1fms | Jitter=%.1fms | Loss=%.0f%%",
				stepNum, totalExpectedSteps, pingRes.MedianMs, pingRes.TrimmedMeanMs, pingRes.PureNetworkMs, pingRes.JitterMs, pingRes.LossRate*100)
		}

		// 3. High-precision 4 MB Stream Download from VPS
		o.log("[Optimizer] [%d/%d] Measuring 4 MB stream download with CRC32 verification...", stepNum, totalExpectedSteps)
		speedRes, err := RunSpeedBenchmark(o.httpClient, o.pool, o.token, nil)
		if err != nil {
			o.log("[Optimizer] Warning: speed benchmark issue: %v", err)
		} else {
			crcMsg := "CRC-OK"
			if !speedRes.CRCVerified {
				crcMsg = "CRC-Error"
			}
			o.log("[Optimizer] [%d/%d] Speed Metrics: %.2f MB/s (%.1f Mbps) | Peak: %.2f MB/s | Stability: %.0f%% [%s]",
				stepNum, totalExpectedSteps, speedRes.SpeedMBps, speedRes.SpeedMbpsNet, speedRes.PeakSpeedMB, speedRes.StabilityPct, crcMsg)
		}

		// 4. Calculate score
		score := o.CalculateScore(profile, pingRes, speedRes)
		isBest := false
		if score > bestScore {
			bestScore = score
			bestCand = cand
			bestPing = pingRes
			bestSpeed = speedRes
			isBest = true
		}

		o.log("[Optimizer] [%d/%d] Score: %.2f (Best so far: %.2f)%s",
			stepNum, totalExpectedSteps, score, bestScore, func() string {
				if isBest {
					return " ★ NEW BEST ★"
				}
				return ""
			}())

		res := StepResult{
			Step:        stepNum,
			TotalSteps:  totalExpectedSteps,
			Candidate:   cand,
			Ping:        pingRes,
			Speed:       speedRes,
			Score:       score,
			IsBestSoFar: isBest,
			CurrentBest: bestScore,
		}
		history = append(history, res)

		if onStep != nil {
			onStep(res)
		}
	}

	// Run Tier 1
	for i, cand := range tier1Candidates {
		if o.cancelFlag.Load() || ctx.Err() != nil {
			o.log("[Optimizer] Optimization cancelled.")
			break
		}
		evaluateCandidate(cand, i)
	}

	// Run Tier 2: Fine-Tuning around the winning candidate
	if bestScore > 0 && !o.cancelFlag.Load() && ctx.Err() == nil {
		o.log("─────────────────────────────────────────────────────────────────────")
		o.log("[Optimizer] Phase 2: Fine-Tuning micro-parameters around winning candidate...")
		o.log("─────────────────────────────────────────────────────────────────────")

		fineCandidates := o.GenerateFineTuningCandidates(bestCand)
		baseIdx := len(tier1Candidates)
		for j, fc := range fineCandidates {
			if o.cancelFlag.Load() || ctx.Err() != nil {
				break
			}
			evaluateCandidate(fc, baseIdx+j)
		}
	}

	report := &OptimizationReport{
		Profile:       profile,
		BestScore:     bestScore,
		BestCandidate: bestCand,
		BestClient:    bestCand.ClientTuning,
		BestServer:    bestCand.ServerTuning,
		BestPing:      bestPing,
		BestSpeed:     bestSpeed,
		TotalSteps:    len(history),
		Duration:      time.Since(startTime),
		StepHistory:   history,
	}

	if bestScore > 0 {
		o.log("═════════════════════════════════════════════════════════════════════")
		o.log("[Optimizer] DEEP OPTIMIZATION FINISHED WITH HIGHEST PRECISION!")
		o.log("[Optimizer] Optimal Candidate: %s", bestCand.Name)
		o.log("[Optimizer] Final Score: %.2f | Median Ping: %.1fms | Speed: %.2f MB/s (%.1f Mbps)",
			bestScore, bestPing.MedianMs, bestSpeed.SpeedMBps, bestSpeed.SpeedMbpsNet)
		o.log("[Optimizer] Applying and persisting optimal parameters to VPS and PC...")
		o.log("═════════════════════════════════════════════════════════════════════")

		// Commit optimal tuning permanently to VPS
		_, _ = SendAdminCommand(o.httpClient, o.pool, o.adminToken, AdminRequest{
			Action: "set_tuning",
			Tuning: &bestCand.ServerTuning,
		})
		_, _ = SendAdminCommand(o.httpClient, o.pool, o.adminToken, AdminRequest{
			Action: "save_config",
		})
		_ = o.client.SetTuning(bestCand.ClientTuning)
	}

	return report, nil
}
