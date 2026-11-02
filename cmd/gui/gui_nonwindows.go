//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"google_script_relay/pkg/relay"
)

type NonWindowsApp struct {
	client     *relay.Client
	cfg        relay.Config
	configPath string
	relayMutex sync.Mutex
	server     *http.Server
	optActive  atomic.Bool
	lastReport *relay.OptimizationReport
	lastStep   *relay.StepResult
	stateMu    sync.RWMutex
}

func runApp() {
	app := &NonWindowsApp{}
	app.init()
}

func (a *NonWindowsApp) init() {
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		p1 := filepath.Join(dir, "client_config.json")
		p2 := filepath.Join(dir, "config", "client_config.json")
		if _, err := os.Stat(p1); err == nil {
			a.configPath = p1
		} else if _, err := os.Stat(p2); err == nil {
			a.configPath = p2
		} else {
			a.configPath = p1
		}
	} else {
		a.configPath = "client_config.json"
	}

	cfg := relay.Config{
		Mode:       "client",
		ListenAddr: "127.0.0.1:10800",
		Token:      "IK13eIodrB24",
		SocksPort:  10808,
		HttpPort:   10809,
		StartXray:  true,
	}

	data, err := os.ReadFile(a.configPath)
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	cfg.EnsureDefaults()

	a.cfg = cfg
	a.client = relay.NewClient(&cfg)
	a.client.LogFunc = func(format string, args ...any) {
		log.Printf(format, args...)
	}

	if len(cfg.GetGasURLs()) > 0 {
		_ = a.client.Start()
	}

	// Find free local port for UI server
	uiPort := 10850
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	if err != nil {
		listener, _ = net.Listen("tcp", "127.0.0.1:0")
		if listener != nil {
			uiPort = listener.Addr().(*net.TCPAddr).Port
		}
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", uiPort)
	log.Printf("[Desktop GUI] Starting desktop UI server on %s", url)

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/start", a.handleStart)
	mux.HandleFunc("/api/stop", a.handleStop)
	mux.HandleFunc("/api/tune/start", a.handleTuneStart)
	mux.HandleFunc("/api/tune/status", a.handleTuneStatus)
	mux.HandleFunc("/api/tune/ping", a.handleQuickPing)
	mux.HandleFunc("/api/tune/speed", a.handleQuickSpeed)
	mux.HandleFunc("/api/config", a.handleConfig)

	a.server = &http.Server{Handler: mux}

	go func() {
		if listener != nil {
			_ = a.server.Serve(listener)
		}
	}()

	// Launch native desktop standalone window
	go a.openNativeWindow(url)

	// Block main thread until signal or exit
	select {}
}

func (a *NonWindowsApp) openNativeWindow(url string) {
	time.Sleep(300 * time.Millisecond)

	var candidates []string
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		)
	} else {
		// Linux
		candidates = append(candidates,
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
			"brave-browser",
			"microsoft-edge",
			"epiphany",
		)
	}

	// Try launching in standalone app mode (no browser address bar/tabs)
	for _, bin := range candidates {
		path, err := exec.LookPath(bin)
		if err == nil {
			cmd := exec.Command(path,
				fmt.Sprintf("--app=%s", url),
				"--window-size=780,680",
				"--class=XrayGasRelay",
				"--user-data-dir=/tmp/xray-gas-relay-profile",
			)
			if cmd.Start() == nil {
				return
			}
		}
	}

	// Fallback to default OS opener if no Chromium-based app mode was found
	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", url).Start()
	} else {
		_ = exec.Command("xdg-open", url).Start()
	}
}

func (a *NonWindowsApp) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(desktopHTML))
}

func (a *NonWindowsApp) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.relayMutex.Lock()
	running := a.client != nil && a.client.IsRunning()
	poolSummary := ""
	if a.client != nil {
		poolSummary = a.client.GetPoolStatus()
	}
	cTuning := relay.DefaultClientTuning()
	if a.client != nil {
		cTuning = a.client.GetTuning()
	}
	urlsCount := 0
	if a.client != nil && a.client.GetEndpointPool() != nil {
		urlsCount = len(a.cfg.GetGasURLs())
	}
	a.relayMutex.Unlock()

	type StatusRes struct {
		Running     bool               `json:"running"`
		PoolSummary string             `json:"pool_summary"`
		Tuning      relay.ClientTuning `json:"tuning"`
		OptRunning  bool               `json:"opt_running"`
		Accounts    int                `json:"accounts"`
		OS          string             `json:"os"`
	}

	res := StatusRes{
		Running:     running,
		PoolSummary: poolSummary,
		Tuning:      cTuning,
		OptRunning:  a.optActive.Load(),
		Accounts:    urlsCount,
		OS:          runtime.GOOS,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (a *NonWindowsApp) handleStart(w http.ResponseWriter, r *http.Request) {
	a.relayMutex.Lock()
	defer a.relayMutex.Unlock()

	if a.client == nil || !a.client.IsRunning() {
		_ = a.client.Start()
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (a *NonWindowsApp) handleStop(w http.ResponseWriter, r *http.Request) {
	a.relayMutex.Lock()
	defer a.relayMutex.Unlock()

	if a.client != nil && a.client.IsRunning() {
		a.client.Stop()
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (a *NonWindowsApp) handleTuneStart(w http.ResponseWriter, r *http.Request) {
	if a.optActive.Load() {
		http.Error(w, "Optimization already running", http.StatusConflict)
		return
	}

	profileStr := r.URL.Query().Get("profile")
	profile := relay.ProfileBalanced
	switch profileStr {
	case "ping":
		profile = relay.ProfilePing
	case "speed":
		profile = relay.ProfileSpeed
	case "balanced":
		profile = relay.ProfileBalanced
	}

	a.optActive.Store(true)
	opt := relay.NewOptimizer(a.client)

	go func() {
		defer a.optActive.Store(false)
		report, _ := opt.RunOptimization(context.Background(), profile, func(sr relay.StepResult) {
			a.stateMu.Lock()
			a.lastStep = &sr
			a.stateMu.Unlock()
		})
		a.stateMu.Lock()
		a.lastReport = report
		a.stateMu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (a *NonWindowsApp) handleTuneStatus(w http.ResponseWriter, r *http.Request) {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()

	type TuneStatusRes struct {
		Running    bool                      `json:"running"`
		LastStep   *relay.StepResult         `json:"last_step,omitempty"`
		LastReport *relay.OptimizationReport `json:"last_report,omitempty"`
	}

	res := TuneStatusRes{
		Running:    a.optActive.Load(),
		LastStep:   a.lastStep,
		LastReport: a.lastReport,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (a *NonWindowsApp) handleQuickPing(w http.ResponseWriter, r *http.Request) {
	if a.client == nil || !a.client.IsRunning() {
		_ = a.client.Start()
	}
	res, _ := relay.RunPingBenchmark(a.client.GetHTTPClient(), a.client.GetEndpointPool(), a.cfg.Token, 6)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (a *NonWindowsApp) handleQuickSpeed(w http.ResponseWriter, r *http.Request) {
	if a.client == nil || !a.client.IsRunning() {
		_ = a.client.Start()
	}
	res, _ := relay.RunSpeedBenchmark(a.client.GetHTTPClient(), a.client.GetEndpointPool(), a.cfg.Token, nil)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (a *NonWindowsApp) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.client.GetTuning())
		return
	}
	if r.Method == http.MethodPost {
		var cfg relay.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err == nil {
			if cfg.ClientTuning != nil {
				a.client.SetTuning(*cfg.ClientTuning)
			}
			out, _ := json.MarshalIndent(cfg, "", "  ")
			_ = os.WriteFile(a.configPath, out, 0644)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}
}

const desktopHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Xray Google Script Relay - Desktop Control Center</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    :root {
      --bg: #141826;
      --card: #1e2334;
      --card-border: #2a3147;
      --primary: #3b82f6;
      --success: #10b981;
      --danger: #ef4444;
      --text: #f8fafc;
      --text-muted: #94a3b8;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
    body { background: var(--bg); color: var(--text); padding: 20px; }
    .container { max-width: 760px; margin: 0 auto; }
    .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
    .title { font-size: 20px; font-weight: 700; display: flex; align-items: center; gap: 8px; }
    .badge { padding: 4px 12px; border-radius: 999px; font-size: 13px; font-weight: 600; }
    .badge-on { background: rgba(16, 185, 129, 0.2); color: var(--success); border: 1px solid var(--success); }
    .badge-off { background: rgba(239, 68, 68, 0.2); color: var(--danger); border: 1px solid var(--danger); }
    .tabs { display: flex; gap: 8px; margin-bottom: 20px; border-bottom: 1px solid var(--card-border); padding-bottom: 10px; }
    .tab-btn { background: transparent; border: none; color: var(--text-muted); font-size: 14px; font-weight: 600; padding: 8px 16px; border-radius: 8px; cursor: pointer; }
    .tab-btn.active { background: var(--card); color: var(--primary); border: 1px solid var(--card-border); }
    .card { background: var(--card); border: 1px solid var(--card-border); border-radius: 12px; padding: 18px; margin-bottom: 16px; }
    .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; margin-bottom: 16px; }
    .metric-title { font-size: 12px; color: var(--text-muted); text-transform: uppercase; margin-bottom: 4px; }
    .metric-value { font-size: 24px; font-weight: 800; color: var(--primary); }
    .btn { padding: 9px 18px; border-radius: 8px; border: none; font-weight: 600; font-size: 13px; cursor: pointer; transition: 0.2s; }
    .btn-primary { background: var(--primary); color: #fff; }
    .btn-danger { background: var(--danger); color: #fff; }
    .btn-outline { background: transparent; border: 1px solid var(--card-border); color: var(--text); }
    .btn-active { border-color: var(--primary); color: var(--primary); }
    .progress-wrap { width: 100%; height: 8px; background: #0f121d; border-radius: 999px; overflow: hidden; margin: 12px 0; }
    .progress-bar { height: 100%; background: var(--primary); width: 0%; transition: width 0.3s; }
    .log-area { width: 100%; height: 260px; background: #0a0d14; border: 1px solid var(--card-border); border-radius: 8px; padding: 12px; color: #a5b4fc; font-family: monospace; font-size: 12px; resize: none; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <div class="title">⚡ Xray Google Script Relay</div>
      <div id="statusBadge" class="badge badge-off">Stopped</div>
    </div>

    <div class="tabs">
      <button class="tab-btn active" onclick="showTab(0)">🚀 Dashboard</button>
      <button class="tab-btn" onclick="showTab(1)">⚡ AI Optimizer</button>
      <button class="tab-btn" onclick="showTab(2)">🌐 Accounts</button>
      <button class="tab-btn" onclick="showTab(3)">⚙️ Dials</button>
    </div>

    <!-- Tab 0: Dashboard -->
    <div id="tab0">
      <div class="grid">
        <div class="card">
          <div class="metric-title">Median Ping</div>
          <div id="valPing" class="metric-value">-- ms</div>
        </div>
        <div class="card">
          <div class="metric-title">Download Speed</div>
          <div id="valSpeed" class="metric-value">-- MB/s</div>
        </div>
        <div class="card">
          <div class="metric-title">Active Accounts</div>
          <div id="valAccounts" class="metric-value">--</div>
        </div>
      </div>

      <div class="card">
        <div style="display: flex; gap: 10px; margin-bottom: 14px;">
          <button class="btn btn-primary" onclick="startRelay()">▶ Start Relay + Xray</button>
          <button class="btn btn-danger" onclick="stopRelay()">⏹ Stop Relay</button>
        </div>
        <div style="font-size: 13px; color: var(--text-muted);">
          Endpoints: <code>SOCKS5: 127.0.0.1:10808</code> | <code>HTTP: 127.0.0.1:10809</code>
        </div>
      </div>

      <div class="card">
        <div style="font-size: 13px; font-weight: 600; margin-bottom: 8px;">Google Script Pool Health</div>
        <div id="poolStatus" style="font-family: monospace; font-size: 12px; color: var(--text-muted);">Waiting for pool...</div>
      </div>
    </div>

    <!-- Tab 1: AI Optimizer -->
    <div id="tab1" style="display: none;">
      <div class="card">
        <div style="font-size: 14px; font-weight: 700; margin-bottom: 12px;">Two-Stage Matrix Network Optimizer</div>
        <div style="display: flex; gap: 8px; margin-bottom: 14px;">
          <button id="pPing" class="btn btn-outline" onclick="setProfile('ping')">1. Best Ping</button>
          <button id="pSpeed" class="btn btn-outline" onclick="setProfile('speed')">2. Best Speed</button>
          <button id="pBal" class="btn btn-outline btn-active" onclick="setProfile('balanced')">3. Balanced [*]</button>
        </div>
        <button class="btn btn-primary" onclick="startTune()">⚡ Run Two-Stage Optimization</button>

        <div class="progress-wrap"><div id="tuneProg" class="progress-bar"></div></div>
        <div id="tuneDesc" style="font-size: 12px; color: var(--text-muted); margin-top: 6px;">Idle. Select profile and start.</div>
      </div>
    </div>

    <!-- Tab 2: Accounts -->
    <div id="tab2" style="display: none;">
      <div class="card">
        <div style="font-size: 13px; font-weight: 600; margin-bottom: 8px;">Google Apps Script Web App URLs</div>
        <textarea id="editUrls" class="log-area" style="height: 140px;"></textarea>
      </div>
    </div>

    <!-- Tab 3: Dials -->
    <div id="tab3" style="display: none;">
      <div class="card">
        <div style="font-size: 13px; font-weight: 600; margin-bottom: 8px;">Engine Parameters</div>
        <div style="font-size: 13px; color: var(--text-muted);">Configure Workers, Hold times, and Mux concurrency.</div>
      </div>
    </div>
  </div>

  <script>
    let activeProfile = 'balanced';
    function showTab(n) {
      for (let i = 0; i < 4; i++) {
        document.getElementById('tab' + i).style.display = (i === n) ? 'block' : 'none';
        document.querySelectorAll('.tab-btn')[i].classList.toggle('active', i === n);
      }
    }
    function setProfile(p) {
      activeProfile = p;
      document.getElementById('pPing').classList.toggle('btn-active', p === 'ping');
      document.getElementById('pSpeed').classList.toggle('btn-active', p === 'speed');
      document.getElementById('pBal').classList.toggle('btn-active', p === 'balanced');
    }
    async function startRelay() { await fetch('/api/start'); updateStatus(); }
    async function stopRelay() { await fetch('/api/stop'); updateStatus(); }
    async function startTune() { await fetch('/api/tune/start?profile=' + activeProfile); }

    async function updateStatus() {
      try {
        const res = await fetch('/api/status');
        const data = await res.json();
        const badge = document.getElementById('statusBadge');
        badge.textContent = data.running ? 'Connected' : 'Stopped';
        badge.className = 'badge ' + (data.running ? 'badge-on' : 'badge-off');
        document.getElementById('valAccounts').textContent = data.accounts;
        document.getElementById('poolStatus').textContent = data.pool_summary || 'Pool active.';
      } catch (e) {}

      try {
        const res = await fetch('/api/tune/status');
        const data = await res.json();
        if (data.running && data.last_step) {
          const s = data.last_step;
          const pct = Math.round((s.Step / s.TotalSteps) * 100);
          document.getElementById('tuneProg').style.width = pct + '%';
          document.getElementById('tuneDesc').textContent = 'Step ' + s.Step + '/' + s.TotalSteps + ': ' + s.Candidate.Name;
          if (s.Ping && s.Ping.median_ms) document.getElementById('valPing').textContent = s.Ping.median_ms.toFixed(1) + ' ms';
          if (s.Speed && s.Speed.speed_mbps) document.getElementById('valSpeed').textContent = s.Speed.speed_mbps.toFixed(2) + ' MB/s';
        } else if (data.last_report) {
          document.getElementById('tuneProg').style.width = '100%';
          document.getElementById('tuneDesc').textContent = 'Complete! Optimal: ' + data.last_report.BestCandidate.Name;
        }
      } catch (e) {}
    }
    setInterval(updateStatus, 1500);
    updateStatus();
  </script>
</body>
</html>
`
