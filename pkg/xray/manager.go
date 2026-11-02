package xray

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
)

const (
	DefaultServerUUID = "e2a4a9c8-7c8b-4a5e-9e2a-1a2b3c4d5e6f"
)

type Manager struct {
	xrayPath   string
	configPath string
	socksPort  int
	httpPort   int
	relayPort  int
	targetPort int
	muxCount   int
	cmd        *exec.Cmd
	running    atomic.Bool
	mu         sync.Mutex
	LogFunc    func(format string, args ...any)
}

func NewManager(socksPort, httpPort, relayPort, muxCount int, logFunc func(format string, args ...any)) *Manager {
	if socksPort <= 0 {
		socksPort = 10808
	}
	if httpPort <= 0 {
		httpPort = 10809
	}
	if relayPort <= 0 {
		relayPort = 10800
	}

	return &Manager{
		socksPort: socksPort,
		httpPort:  httpPort,
		relayPort: relayPort,
		muxCount:  muxCount,
		LogFunc:   logFunc,
	}
}

func NewServerManager(targetPort int, logFunc func(format string, args ...any)) *Manager {
	if targetPort <= 0 {
		targetPort = 10001
	}
	return &Manager{
		targetPort: targetPort,
		LogFunc:    logFunc,
	}
}

func (m *Manager) log(format string, args ...any) {
	if m.LogFunc != nil {
		m.LogFunc(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

func (m *Manager) FindXrayExecutable() (string, error) {
	selfPath, err := os.Executable()
	var candidates []string
	if err == nil {
		dir := filepath.Dir(selfPath)
		candidates = append(candidates,
			filepath.Join(dir, "bin", "xray.exe"),
			filepath.Join(dir, "xray.exe"),
			filepath.Join(dir, "..", "bin", "xray.exe"),
			filepath.Join(dir, "bin", "xray"),
			filepath.Join(dir, "xray"),
		)
	}

	candidates = append(candidates,
		`D:\Projects\google_script_relay\bin\xray.exe`,
		`D:\Projects\g2reverse\xray.exe`,
		`C:\PattN-windows-64\PattN-windows-64\bin\xray\xray.exe`,
		`/usr/local/bin/xray`,
		`/usr/bin/xray`,
		`/opt/xray/xray`,
	)

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}

	if p, err := exec.LookPath("xray.exe"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("xray"); err == nil {
		return p, nil
	}

	// If on Linux and not found, attempt automatic download from official releases
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		m.log("[Xray Core] Xray binary not found. Downloading official Xray-core for Linux amd64...")
		downloadedPath, dlErr := downloadLinuxXray()
		if dlErr == nil {
			m.log("[Xray Core] Downloaded and unpacked Xray to %s", downloadedPath)
			return downloadedPath, nil
		}
		m.log("[Xray Core] Auto-download failed: %v", dlErr)
	}

	return "", fmt.Errorf("xray executable not found in bin directory or system PATH")
}

func downloadLinuxXray() (string, error) {
	selfPath, _ := os.Executable()
	targetDir := filepath.Join(filepath.Dir(selfPath), "bin")
	_ = os.MkdirAll(targetDir, 0755)

	zipURL := "https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-64.zip"
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(zipURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", err
	}

	var extractedPath string
	for _, f := range zr.File {
		if f.Name == "xray" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			outPath := filepath.Join(targetDir, "xray")
			outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				rc.Close()
				return "", err
			}
			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				return "", err
			}
			extractedPath = outPath
			break
		}
	}

	if extractedPath == "" {
		return "", fmt.Errorf("xray binary not found in zip archive")
	}

	_ = os.Chmod(extractedPath, 0755)
	return extractedPath, nil
}

func (m *Manager) GenerateConfig(muxConcurrency int) (string, error) {
	selfPath, _ := os.Executable()
	dir := filepath.Dir(selfPath)
	configDir := filepath.Join(dir, "config")
	_ = os.MkdirAll(configDir, 0755)

	configPath := filepath.Join(configDir, "xray_client.json")

	type User struct {
		ID         string `json:"id"`
		Encryption string `json:"encryption"`
	}
	type Vnext struct {
		Address string `json:"address"`
		Port    int    `json:"port"`
		Users   []User `json:"users"`
	}
	type OutboundSettings struct {
		Vnext []Vnext `json:"vnext"`
	}
	type StreamSettings struct {
		Network string `json:"network"`
	}
	type MuxSettings struct {
		Enabled     bool `json:"enabled"`
		Concurrency int  `json:"concurrency,omitempty"`
	}
	type Outbound struct {
		Protocol       string            `json:"protocol"`
		Settings       *OutboundSettings `json:"settings,omitempty"`
		StreamSettings *StreamSettings   `json:"streamSettings,omitempty"`
		Mux            *MuxSettings      `json:"mux,omitempty"`
		Tag            string            `json:"tag"`
	}
	type InboundSettings struct {
		Auth string `json:"auth,omitempty"`
		UDP  bool   `json:"udp,omitempty"`
	}
	type Inbound struct {
		Port     int              `json:"port"`
		Listen   string           `json:"listen"`
		Protocol string           `json:"protocol"`
		Settings *InboundSettings `json:"settings,omitempty"`
		Tag      string           `json:"tag"`
	}
	type XrayConf struct {
		Log       map[string]string `json:"log"`
		Inbounds  []Inbound         `json:"inbounds"`
		Outbounds []Outbound        `json:"outbounds"`
	}

	mux := &MuxSettings{Enabled: false}
	if muxConcurrency > 0 {
		mux.Enabled = true
		mux.Concurrency = muxConcurrency
	}

	conf := XrayConf{
		Log: map[string]string{"loglevel": "warning"},
		Inbounds: []Inbound{
			{
				Port:     m.socksPort,
				Listen:   "127.0.0.1",
				Protocol: "socks",
				Settings: &InboundSettings{Auth: "noauth", UDP: true},
				Tag:      "socks-in",
			},
			{
				Port:     m.httpPort,
				Listen:   "127.0.0.1",
				Protocol: "http",
				Tag:      "http-in",
			},
		},
		Outbounds: []Outbound{
			{
				Protocol: "vless",
				Settings: &OutboundSettings{
					Vnext: []Vnext{
						{
							Address: "127.0.0.1",
							Port:    m.relayPort,
							Users: []User{
								{ID: DefaultServerUUID, Encryption: "none"},
							},
						},
					},
				},
				StreamSettings: &StreamSettings{Network: "tcp"},
				Mux:            mux,
				Tag:            "proxy",
			},
			{
				Protocol: "freedom",
				Tag:      "direct",
			},
		},
	}

	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return "", err
	}

	m.configPath = configPath
	return configPath, nil
}

func (m *Manager) GenerateServerConfig() (string, error) {
	selfPath, _ := os.Executable()
	dir := filepath.Dir(selfPath)
	configDir := filepath.Join(dir, "config")
	_ = os.MkdirAll(configDir, 0755)

	configPath := filepath.Join(configDir, "xray_server.json")

	type ClientUser struct {
		ID string `json:"id"`
	}
	type InboundSettings struct {
		Clients    []ClientUser `json:"clients"`
		Decryption string       `json:"decryption"`
	}
	type StreamSettings struct {
		Network string `json:"network"`
	}
	type Inbound struct {
		Port           int              `json:"port"`
		Listen         string           `json:"listen"`
		Protocol       string           `json:"protocol"`
		Settings       *InboundSettings `json:"settings"`
		StreamSettings *StreamSettings  `json:"streamSettings"`
	}
	type Outbound struct {
		Protocol string `json:"protocol"`
		Tag      string `json:"tag"`
	}
	type XrayServerConf struct {
		Log       map[string]string `json:"log"`
		Inbounds  []Inbound         `json:"inbounds"`
		Outbounds []Outbound        `json:"outbounds"`
	}

	conf := XrayServerConf{
		Log: map[string]string{"loglevel": "warning"},
		Inbounds: []Inbound{
			{
				Port:     m.targetPort,
				Listen:   "127.0.0.1",
				Protocol: "vless",
				Settings: &InboundSettings{
					Clients: []ClientUser{
						{ID: DefaultServerUUID},
					},
					Decryption: "none",
				},
				StreamSettings: &StreamSettings{
					Network: "tcp",
				},
			},
		},
		Outbounds: []Outbound{
			{
				Protocol: "freedom",
				Tag:      "direct",
			},
		},
	}

	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return "", err
	}

	m.configPath = configPath
	return configPath, nil
}

func (m *Manager) Start(muxConcurrency int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running.Load() {
		return nil
	}

	xrayExe, err := m.FindXrayExecutable()
	if err != nil {
		return err
	}
	m.xrayPath = xrayExe

	cfgPath, err := m.GenerateConfig(muxConcurrency)
	if err != nil {
		return fmt.Errorf("failed to generate xray config: %w", err)
	}

	cmd := exec.Command(m.xrayPath, "run", "-config", cfgPath)
	setProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start xray core: %w", err)
	}

	m.cmd = cmd
	m.running.Store(true)
	m.log("[Xray Core] Client started (SOCKS: 127.0.0.1:%d, HTTP: 127.0.0.1:%d)", m.socksPort, m.httpPort)

	go func() {
		_ = cmd.Wait()
		m.running.Store(false)
		m.log("[Xray Core] Process terminated.")
	}()

	return nil
}

// StartServerAuto launches Xray server if the target inbound is not already listening
func (m *Manager) StartServerAuto() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	targetAddr := fmt.Sprintf("127.0.0.1:%d", m.targetPort)

	// Check if already listening
	conn, err := net.DialTimeout("tcp", targetAddr, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		m.log("[Xray Core] Detected active inbound on %s. Auto-launch skipped.", targetAddr)
		return nil
	}

	m.log("[Xray Core] Port %s is not listening. Launching integrated server Xray...", targetAddr)

	xrayExe, err := m.FindXrayExecutable()
	if err != nil {
		return fmt.Errorf("cannot find or download xray executable: %w", err)
	}
	m.xrayPath = xrayExe

	cfgPath, err := m.GenerateServerConfig()
	if err != nil {
		return fmt.Errorf("failed to generate xray server config: %w", err)
	}

	cmd := exec.Command(m.xrayPath, "run", "-config", cfgPath)
	setProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start server xray process: %w", err)
	}

	m.cmd = cmd
	m.running.Store(true)

	// Wait up to 3 seconds for port to open
	ready := false
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		c, err := net.DialTimeout("tcp", targetAddr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			ready = true
			break
		}
	}

	if ready {
		m.log("[Xray Core] Server Xray is ACTIVE on %s (Protocol: VLESS/TCP, UUID: %s)", targetAddr, DefaultServerUUID)
	} else {
		m.log("[Xray Core] Warning: Server Xray started (PID: %d) but port %s not yet ready.", cmd.Process.Pid, targetAddr)
	}

	go func() {
		_ = cmd.Wait()
		m.running.Store(false)
		m.log("[Xray Core] Server Xray process terminated.")
	}()

	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running.CompareAndSwap(true, false) {
		if m.cmd != nil && m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
		m.log("[Xray Core] Stopped.")
	}
}

func (m *Manager) IsRunning() bool {
	return m.running.Load()
}

func (m *Manager) GetSocksPort() int {
	return m.socksPort
}

func (m *Manager) GetHttpPort() int {
	return m.httpPort
}
