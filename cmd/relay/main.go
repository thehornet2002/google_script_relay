package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google_script_relay/pkg/relay"
)

func main() {
	mode := flag.String("mode", "", "Running mode: client, server, bench-ping, bench-speed, auto-tune, gen-token")
	configFile := flag.String("config", "", "Path to JSON config file (optional)")
	listenAddr := flag.String("listen", "", "Listen address (e.g. 127.0.0.1:10800 for client, 0.0.0.0:8888 for server)")
	targetAddr := flag.String("target", "", "Target address (server only: 127.0.0.1:10001 for VPS Xray)")
	gasURL := flag.String("gas", "", "Google Apps Script Web App URL(s), comma-separated (client only)")
	token := flag.String("token", "", "Shared authentication token")
	adminToken := flag.String("admin-token", "", "Administrative token for remote VPS tuning (server/optimizer)")
	xrayUUID := flag.String("xray-uuid", "", "Custom Xray client/server UUID")
	frontDomain := flag.String("front-domain", "", "Fronting domain (TLS SNI, e.g. fcm.googleapis.com)")
	frontIP := flag.String("front-ip", "", "Fronting clean Google IP (e.g. 142.250.180.188:443)")
	profileStr := flag.String("profile", "balanced", "Optimization profile: ping, speed, balanced")
	startXray := flag.Bool("xray", true, "Start integrated Xray core automatically (client mode)")

	flag.Parse()

	if *mode == "gen-token" {
		t, err := relay.GenerateSecureToken(32)
		if err != nil {
			log.Fatalf("Token generation failed: %v", err)
		}
		u := relay.GenerateUUID()
		fmt.Printf("Secure Token: %s\n", t)
		fmt.Printf("Xray UUID:    %s\n", u)
		return
	}

	cfg := relay.Config{}
	cfgPath := ""
	if *configFile != "" {
		cfgPath = *configFile
		data, err := os.ReadFile(*configFile)
		if err != nil {
			log.Fatalf("Error reading config file: %v", err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Fatalf("Error parsing config file: %v", err)
		}
	}
	cfg.EnsureDefaults()

	if *mode != "" {
		cfg.Mode = *mode
	}
	if *listenAddr != "" {
		cfg.ListenAddr = *listenAddr
	}
	if *targetAddr != "" {
		cfg.TargetAddr = *targetAddr
	}
	if *gasURL != "" {
		parts := strings.Split(*gasURL, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				cfg.GasURLs = append(cfg.GasURLs, trimmed)
			}
		}
	}
	if *token != "" {
		cfg.Token = *token
	}
	if *adminToken != "" {
		cfg.AdminToken = *adminToken
	}
	if *xrayUUID != "" {
		cfg.XrayUUID = *xrayUUID
	}
	if *frontDomain != "" {
		cfg.FrontDomain = *frontDomain
	}
	if *frontIP != "" {
		cfg.FrontIP = *frontIP
	}
	cfg.StartXray = *startXray

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cfg.Mode {
	case "client":
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Client error: %v", err)
		}
		log.Printf("[Client] Press Ctrl+C to terminate.")
		<-ctx.Done()
		log.Printf("[Client] Received termination signal, shutting down...")
		c.Stop()

	case "server":
		s := relay.NewServer(&cfg)
		if cfgPath != "" {
			s.SetConfigPath(cfgPath)
		}

		serverErr := make(chan error, 1)
		go func() {
			serverErr <- s.Start()
		}()

		select {
		case err := <-serverErr:
			if err != nil {
				log.Fatalf("Server error: %v", err)
			}
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.Shutdown(shutdownCtx); err != nil {
				log.Printf("[Server] Shutdown warning: %v", err)
			}
		}

	case "bench-ping":
		cfg.StartXray = false
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Failed to initialize relay: %v", err)
		}
		defer c.Stop()

		res, err := relay.RunPingBenchmark(c.GetHTTPClient(), c.GetEndpointPool(), cfg.Token, 6)
		if err != nil {
			log.Fatalf("Ping benchmark failed: %v", err)
		}
		fmt.Printf("=== Ping Benchmark Results (Warmup + 6 Probes to VPS) ===\n")
		fmt.Printf("Median: %.1fms | Trimmed Avg: %.1fms | PureNet RTT: %.1fms | Min: %.1fms | Max: %.1fms | Jitter: %.1fms | Loss: %.0f%%\n",
			res.MedianMs, res.TrimmedMeanMs, res.PureNetworkMs, res.MinMs, res.MaxMs, res.JitterMs, res.LossRate*100)

	case "bench-speed":
		cfg.StartXray = false
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Failed to initialize relay: %v", err)
		}
		defer c.Stop()

		fmt.Printf("Downloading 4 MB test stream directly from VPS over Google Script tunnel...\n")
		res, err := relay.RunSpeedBenchmark(c.GetHTTPClient(), c.GetEndpointPool(), cfg.Token, func(done, total int, speed float64) {
			pct := float64(done) / float64(total) * 100
			fmt.Printf("\rProgress: %.1f%% (%d / %d bytes) - Instant Speed: %.2f MB/s", pct, done, total, speed)
		})
		fmt.Println()
		if err != nil {
			log.Fatalf("Speed benchmark failed: %v", err)
		}
		crcMsg := "Verified"
		if !res.CRCVerified {
			crcMsg = "FAILED (Corrupted)"
		}
		fmt.Printf("=== 4 MB Download Benchmark Results ===\n")
		fmt.Printf("Sustained Speed: %.2f MB/s (%.1f Mbps) | Peak Burst: %.2f MB/s | Stability: %.0f%% | CRC32: %s | Time: %v\n",
			res.SpeedMBps, res.SpeedMbpsNet, res.PeakSpeedMB, res.StabilityPct, crcMsg, res.Duration.Round(time.Millisecond))

	case "auto-tune":
		cfg.StartXray = false
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Failed to initialize relay: %v", err)
		}
		defer c.Stop()

		opt := relay.NewOptimizer(c)

		profile := relay.ProfileBalanced
		switch strings.ToLower(*profileStr) {
		case "ping":
			profile = relay.ProfilePing
		case "speed":
			profile = relay.ProfileSpeed
		case "balanced":
			profile = relay.ProfileBalanced
		}

		fmt.Printf("Starting two-stage matrix optimization for profile: %s\n", profile)
		report, err := opt.RunOptimization(ctx, profile, func(sr relay.StepResult) {
			fmt.Printf("Step %d/%d [%s] -> Ping: %.1fms, Speed: %.2f MB/s, Score: %.2f\n",
				sr.Step, sr.TotalSteps, sr.Candidate.Name, sr.Ping.MedianMs, sr.Speed.SpeedMBps, sr.Score)
		})
		if err != nil {
			log.Fatalf("Optimization failed: %v", err)
		}
		fmt.Printf("\n=== Optimization Complete ===\n")
		fmt.Printf("Best Score: %.2f | Optimal Candidate: %s | Median Ping: %.1fms | Download: %.2f MB/s\n",
			report.BestScore, report.BestCandidate.Name, report.BestPing.MedianMs, report.BestSpeed.SpeedMBps)

		cfg.ClientTuning = &report.BestClient
		cfg.ServerTuning = &report.BestServer
		if cfgPath != "" {
			out, _ := json.MarshalIndent(cfg, "", "  ")
			if err := relay.AtomicWriteSecureConfigFile(cfgPath, out); err != nil {
				log.Printf("Warning: failed to save config file: %v", err)
			} else {
				fmt.Printf("Saved optimized client config securely to %s\n", cfgPath)
			}
		}

	default:
		log.Fatalf("Invalid mode %q. Use -mode client, server, bench-ping, bench-speed, auto-tune, or gen-token", cfg.Mode)
	}
}
