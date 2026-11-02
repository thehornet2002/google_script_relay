package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"google_script_relay/pkg/relay"
)

func main() {
	mode := flag.String("mode", "", "Running mode: client, server, bench-ping, bench-speed, auto-tune")
	configFile := flag.String("config", "", "Path to JSON config file (optional)")
	listenAddr := flag.String("listen", "", "Listen address (e.g. 127.0.0.1:10800 for client, 0.0.0.0:8888 for server)")
	targetAddr := flag.String("target", "", "Target address (server only: 127.0.0.1:10001 for VPS Xray)")
	gasURL := flag.String("gas", "", "Google Apps Script Web App URL(s), comma-separated (client only)")
	token := flag.String("token", "", "Shared authentication token")
	frontDomain := flag.String("front-domain", "", "Fronting domain (TLS SNI, e.g. fcm.googleapis.com)")
	frontIP := flag.String("front-ip", "", "Fronting clean Google IP (e.g. 142.250.180.188:443)")
	profileStr := flag.String("profile", "balanced", "Optimization profile: ping, speed, balanced")
	startXray := flag.Bool("xray", true, "Start integrated Xray core automatically (client mode)")

	flag.Parse()

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
	if *frontDomain != "" {
		cfg.FrontDomain = *frontDomain
	}
	if *frontIP != "" {
		cfg.FrontIP = *frontIP
	}
	cfg.StartXray = *startXray

	switch cfg.Mode {
	case "client":
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Client error: %v", err)
		}
		select {}

	case "server":
		s := relay.NewServer(&cfg)
		if cfgPath != "" {
			s.SetConfigPath(cfgPath)
		}
		if err := s.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}

	case "bench-ping":
		cfg.StartXray = false
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Failed to initialize relay: %v", err)
		}
		res, err := relay.RunPingBenchmark(c.GetHTTPClient(), c.GetEndpointPool(), cfg.Token, 4)
		if err != nil {
			log.Fatalf("Ping benchmark failed: %v", err)
		}
		fmt.Printf("=== Ping Benchmark Results (4 packets to VPS) ===\n")
		fmt.Printf("Min: %.1fms | Max: %.1fms | Avg: %.1fms | Jitter: %.1fms | Loss: %.0f%%\n",
			res.MinMs, res.MaxMs, res.AvgMs, res.JitterMs, res.LossRate*100)

	case "bench-speed":
		cfg.StartXray = false
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Failed to initialize relay: %v", err)
		}
		fmt.Printf("Downloading 4 MB test stream directly from VPS over Google Script tunnel...\n")
		res, err := relay.RunSpeedBenchmark(c.GetHTTPClient(), c.GetEndpointPool(), cfg.Token, func(done, total int, speed float64) {
			pct := float64(done) / float64(total) * 100
			fmt.Printf("\rProgress: %.1f%% (%d / %d bytes) - Speed: %.2f MB/s", pct, done, total, speed)
		})
		fmt.Println()
		if err != nil {
			log.Fatalf("Speed benchmark failed: %v", err)
		}
		fmt.Printf("=== 4 MB Download Benchmark Results ===\n")
		fmt.Printf("Speed: %.2f MB/s (%.1f Mbps) | Time: %v\n", res.SpeedMBps, res.SpeedMbpsNet, res.Duration.Round(time.Millisecond))

	case "auto-tune":
		cfg.StartXray = false
		c := relay.NewClient(&cfg)
		if err := c.Start(); err != nil {
			log.Fatalf("Failed to initialize relay: %v", err)
		}
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

		fmt.Printf("Starting auto-tuning optimization for profile: %s\n", profile)
		report, err := opt.RunOptimization(context.Background(), profile, func(sr relay.StepResult) {
			fmt.Printf("Step %d/%d [%s] -> Ping: %.1fms, Speed: %.2f MB/s, Score: %.2f\n",
				sr.Step, sr.TotalSteps, sr.Candidate.Name, sr.Ping.AvgMs, sr.Speed.SpeedMBps, sr.Score)
		})
		if err != nil {
			log.Fatalf("Optimization failed: %v", err)
		}
		fmt.Printf("\n=== Optimization Complete ===\n")
		fmt.Printf("Best Score: %.2f | Ping: %.1fms | Download: %.2f MB/s\n",
			report.BestScore, report.BestPing.AvgMs, report.BestSpeed.SpeedMBps)
		cfg.ClientTuning = &report.BestClient
		cfg.ServerTuning = &report.BestServer
		if cfgPath != "" {
			out, _ := json.MarshalIndent(cfg, "", "  ")
			_ = os.WriteFile(cfgPath, out, 0644)
			fmt.Printf("Saved optimized client config to %s\n", cfgPath)
		}

	default:
		log.Fatalf("Invalid mode %q. Use -mode client, server, bench-ping, bench-speed, or auto-tune", cfg.Mode)
	}
}
