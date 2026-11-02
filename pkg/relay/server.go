package relay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google_script_relay/pkg/xray"
)

const (
	BenchmarkFileSize = 4 * 1024 * 1024 // Exact 4 MB test payload
)

var (
	benchPayloadOnce sync.Once
	benchPayload     []byte
	benchPayloadCRC  uint32
)

func getBenchmarkPayload() ([]byte, uint32) {
	benchPayloadOnce.Do(func() {
		benchPayload = make([]byte, BenchmarkFileSize)
		var val byte = 0x5A
		for i := range benchPayload {
			val = (val*33 + 7) & 0xFF
			benchPayload[i] = val
		}
		benchPayloadCRC = crc32.ChecksumIEEE(benchPayload)
	})
	return benchPayload, benchPayloadCRC
}

type ServerSession struct {
	id          string
	xrayConn    net.Conn
	downlinkCh  chan []byte
	downlinkSeq atomic.Uint64
	closed      atomic.Bool
	lastActive  atomic.Int64
	once        sync.Once
}

func (s *ServerSession) Close() {
	s.once.Do(func() {
		s.closed.Store(true)
		if s.xrayConn != nil {
			_ = s.xrayConn.Close()
		}
	})
}

type Server struct {
	cfg        *Config
	configPath string
	sessions   sync.Map // map[string]*ServerSession
	tuningMu   sync.RWMutex
	tuning     ServerTuning
	xrayMgr    *xray.Manager
	startTime  time.Time
}

func NewServer(cfg *Config) *Server {
	cfg.EnsureDefaults()
	s := &Server{
		cfg:       cfg,
		tuning:    *cfg.ServerTuning,
		startTime: time.Now(),
	}
	return s
}

func (s *Server) SetConfigPath(p string) {
	s.configPath = p
}

func (s *Server) GetTuning() ServerTuning {
	s.tuningMu.RLock()
	defer s.tuningMu.RUnlock()
	return s.tuning
}

func (s *Server) SetTuning(t ServerTuning) {
	s.tuningMu.Lock()
	defer s.tuningMu.Unlock()
	if t.WaitTimeoutMs < 100 {
		t.WaitTimeoutMs = 100
	}
	if t.MaxDrainBatchBytes < 8192 {
		t.MaxDrainBatchBytes = 8192
	}
	s.tuning = t
	log.Printf("[Server] Updated tuning: WaitTimeout=%dms, MaxDrain=%d bytes", t.WaitTimeoutMs, t.MaxDrainBatchBytes)
}

func (s *Server) Start() error {
	if s.cfg.ListenAddr == "" {
		s.cfg.ListenAddr = "0.0.0.0:8080"
	}
	if s.cfg.TargetAddr == "" {
		s.cfg.TargetAddr = "127.0.0.1:10001"
	}

	go s.reapInactiveSessions(60 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", s.handleTunnel)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	log.Printf("[Server] Listening on %s", s.cfg.ListenAddr)
	log.Printf("[Server] Forwarding target (VPS Xray): %s", s.cfg.TargetAddr)
	t := s.GetTuning()
	log.Printf("[Server] Initial Tuning: WaitTimeout=%dms, MaxDrain=%d bytes", t.WaitTimeoutMs, t.MaxDrainBatchBytes)

	// Automatically verify or start server-side Xray inbound on target port
	_, portStr, err := net.SplitHostPort(s.cfg.TargetAddr)
	targetPort := 10001
	if err == nil {
		fmt.Sscanf(portStr, "%d", &targetPort)
	}
	s.xrayMgr = xray.NewServerManager(targetPort, log.Printf)
	if err := s.xrayMgr.StartServerAuto(); err != nil {
		log.Printf("[Server] Auto-Xray note: %v", err)
	}

	server := &http.Server{
		Addr:         s.cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  35 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server.ListenAndServe()
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	rxTime := time.Now().UnixNano()

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var p Packet
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if s.cfg.Token != "" && p.Token != s.cfg.Token {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 1. High-precision ping benchmark: reports nano timestamps to measure pure network latency
	if p.SessionID == "__ping__" {
		txTime := time.Now().UnixNano()
		jsonResponse(w, ResponsePacket{
			ServerRxNano: rxTime,
			ServerTxNano: txTime,
			Timestamp:    txTime / int64(time.Millisecond),
		})
		return
	}

	// 2. High-precision 4 MB benchmark streaming directly from VPS
	if p.SessionID == "__bench_4mb__" {
		s.handleBenchmarkStream(w, p)
		return
	}

	// 3. Fast path for remote admin & tuning
	if p.SessionID == "__admin__" {
		s.handleAdminRequest(w, p)
		return
	}

	if p.SessionID == "" {
		http.Error(w, "Missing Session ID", http.StatusBadRequest)
		return
	}

	sess := s.getOrCreateSession(p.SessionID)
	if sess == nil {
		jsonResponse(w, ResponsePacket{Close: true, Error: "failed to connect target"})
		return
	}
	sess.lastActive.Store(time.Now().Unix())

	if p.Close {
		sess.Close()
		s.sessions.Delete(p.SessionID)
		jsonResponse(w, ResponsePacket{Close: true})
		return
	}

	// Write incoming client data to VPS Xray
	if p.Data != "" {
		raw, err := base64.StdEncoding.DecodeString(p.Data)
		if err == nil && len(raw) > 0 {
			if _, wErr := sess.xrayConn.Write(raw); wErr != nil {
				sess.Close()
				s.sessions.Delete(p.SessionID)
				jsonResponse(w, ResponsePacket{Close: true})
				return
			}
		}
	}

	// Fetch downlink data for client according to current ServerTuning
	tuning := s.GetTuning()
	var combined []byte
	waitTimeout := time.Duration(tuning.WaitTimeoutMs) * time.Millisecond
	if p.Data != "" {
		waitTimeout = 10 * time.Millisecond
	}

	select {
	case chunk, ok := <-sess.downlinkCh:
		if !ok {
			jsonResponse(w, ResponsePacket{Close: true})
			return
		}
		combined = append(combined, chunk...)
	drainLoop:
		for len(combined) < tuning.MaxDrainBatchBytes {
			select {
			case extra, ok := <-sess.downlinkCh:
				if !ok {
					break drainLoop
				}
				combined = append(combined, extra...)
			default:
				break drainLoop
			}
		}
	case <-time.After(waitTimeout):
	}

	if sess.closed.Load() {
		s.sessions.Delete(p.SessionID)
		jsonResponse(w, ResponsePacket{Close: true})
		return
	}

	resp := ResponsePacket{}
	if len(combined) > 0 {
		resp.Seq = sess.downlinkSeq.Add(1)
		resp.Data = base64.StdEncoding.EncodeToString(combined)
	}
	jsonResponse(w, resp)
}

func (s *Server) handleBenchmarkStream(w http.ResponseWriter, p Packet) {
	offset := 0
	if p.Data != "" {
		decoded, err := base64.StdEncoding.DecodeString(p.Data)
		if err == nil {
			if parsed, err := strconv.Atoi(string(decoded)); err == nil {
				offset = parsed
			}
		}
	}

	all, _ := getBenchmarkPayload()
	if offset >= len(all) {
		jsonResponse(w, ResponsePacket{
			TotalSize: len(all),
			Offset:    len(all),
			Close:     true,
		})
		return
	}

	chunkSize := s.GetTuning().MaxDrainBatchBytes
	end := offset + chunkSize
	if end > len(all) {
		end = len(all)
	}

	chunk := all[offset:end]
	chunkCRC := crc32.ChecksumIEEE(chunk)

	jsonResponse(w, ResponsePacket{
		TotalSize: len(all),
		Offset:    end,
		CRC32:     chunkCRC,
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})
}

func (s *Server) handleAdminRequest(w http.ResponseWriter, p Packet) {
	raw, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		jsonResponse(w, ResponsePacket{Error: "invalid base64 admin request"})
		return
	}

	var req AdminRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		jsonResponse(w, ResponsePacket{Error: "invalid json admin request"})
		return
	}

	res := AdminResponse{Success: true}

	switch req.Action {
	case "get_tuning":
		t := s.GetTuning()
		res.Tuning = &t
	case "set_tuning":
		if req.Tuning != nil {
			s.SetTuning(*req.Tuning)
			t := s.GetTuning()
			res.Tuning = &t
			res.Message = "Tuning applied successfully"
		} else {
			res.Success = false
			res.Message = "Missing tuning object"
		}
	case "save_config":
		if s.configPath != "" {
			t := s.GetTuning()
			s.cfg.ServerTuning = &t
			data, err := json.MarshalIndent(s.cfg, "", "  ")
			if err == nil {
				_ = os.WriteFile(s.configPath, data, 0644)
				res.Message = fmt.Sprintf("Saved server config to %s", s.configPath)
			} else {
				res.Success = false
				res.Message = err.Error()
			}
		}
	case "status":
		t := s.GetTuning()
		res.Tuning = &t
		res.Uptime = int64(time.Since(s.startTime).Seconds())
	default:
		res.Success = false
		res.Message = "Unknown action: " + req.Action
	}

	out, _ := json.Marshal(res)
	jsonResponse(w, ResponsePacket{
		Data: base64.StdEncoding.EncodeToString(out),
	})
}

func (s *Server) getOrCreateSession(id string) *ServerSession {
	val, ok := s.sessions.Load(id)
	if ok {
		sess := val.(*ServerSession)
		if !sess.closed.Load() {
			return sess
		}
		s.sessions.Delete(id)
	}

	conn, err := net.DialTimeout("tcp", s.cfg.TargetAddr, 5*time.Second)
	if err != nil {
		log.Printf("[Server] Dial target %s error: %v", s.cfg.TargetAddr, err)
		return nil
	}

	tuning := s.GetTuning()
	qCap := tuning.DownlinkQueueCap
	if qCap < 128 {
		qCap = 256
	}

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
		_ = tcp.SetReadBuffer(65536)
		_ = tcp.SetWriteBuffer(65536)
	}

	sess := &ServerSession{
		id:         id,
		xrayConn:   conn,
		downlinkCh: make(chan []byte, qCap),
	}
	sess.lastActive.Store(time.Now().Unix())

	s.sessions.Store(id, sess)

	go func() {
		defer func() {
			close(sess.downlinkCh)
			sess.Close()
		}()

		buf := make([]byte, 32768)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case sess.downlinkCh <- data:
				case <-time.After(5 * time.Second):
					log.Printf("[Server] Session %s downlink buffer overflow, closing", id)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return sess
}

func (s *Server) reapInactiveSessions(timeout time.Duration) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()
		s.sessions.Range(func(key, value any) bool {
			sess := value.(*ServerSession)
			if now-sess.lastActive.Load() > int64(timeout.Seconds()) {
				log.Printf("[Server] Reaping inactive session %s", sess.id)
				sess.Close()
				s.sessions.Delete(key)
			}
			return true
		})
	}
}

func jsonResponse(w http.ResponseWriter, resp ResponsePacket) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
