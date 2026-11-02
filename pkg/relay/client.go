package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"google_script_relay/pkg/xray"
)

type ClientSession struct {
	id          string
	tcpConn     net.Conn
	closed      atomic.Bool
	wakeDown    chan struct{}
	preferredEP *Endpoint
	epMu        sync.RWMutex

	reorderMu  sync.Mutex
	nextSeq    uint64
	reorderBuf map[uint64][]byte

	once sync.Once
}

func (s *ClientSession) Close() {
	s.once.Do(func() {
		s.closed.Store(true)
		if s.tcpConn != nil {
			_ = s.tcpConn.Close()
		}
	})
}

func (s *ClientSession) GetEndpoint() *Endpoint {
	s.epMu.RLock()
	defer s.epMu.RUnlock()
	return s.preferredEP
}

func (s *ClientSession) SetEndpoint(ep *Endpoint) {
	s.epMu.Lock()
	s.preferredEP = ep
	s.epMu.Unlock()
}

func (s *ClientSession) TriggerWake() {
	select {
	case s.wakeDown <- struct{}{}:
	default:
	}
}

func (s *ClientSession) WriteInOrder(seq uint64, data []byte) error {
	s.reorderMu.Lock()
	defer s.reorderMu.Unlock()

	if s.closed.Load() {
		return fmt.Errorf("session closed")
	}

	if seq == 0 {
		_, err := s.tcpConn.Write(data)
		return err
	}

	if seq == s.nextSeq {
		if _, err := s.tcpConn.Write(data); err != nil {
			return err
		}
		s.nextSeq++

		for {
			nextData, found := s.reorderBuf[s.nextSeq]
			if !found {
				break
			}
			delete(s.reorderBuf, s.nextSeq)
			if _, err := s.tcpConn.Write(nextData); err != nil {
				return err
			}
			s.nextSeq++
		}
		return nil
	}

	if seq > s.nextSeq {
		s.reorderBuf[seq] = data
	}
	return nil
}

type Client struct {
	cfg        *Config
	listener   net.Listener
	httpClient *http.Client
	pool       *EndpointPool
	xrayMgr    *xray.Manager
	tuningMu   sync.RWMutex
	tuning     ClientTuning
	running    atomic.Bool
	LogFunc    func(format string, args ...any)
}

func NewClient(cfg *Config) *Client {
	cfg.EnsureDefaults()
	c := &Client{
		cfg:    cfg,
		tuning: *cfg.ClientTuning,
	}
	return c
}

func (c *Client) log(format string, args ...any) {
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

func (c *Client) GetTuning() ClientTuning {
	c.tuningMu.RLock()
	defer c.tuningMu.RUnlock()
	return c.tuning
}

func (c *Client) SetTuning(t ClientTuning) {
	c.tuningMu.Lock()
	defer c.tuningMu.Unlock()
	if t.Workers < 1 {
		t.Workers = 1
	}
	if t.Workers > 4 {
		t.Workers = 4
	}
	if t.SocketBufferKB < 16 {
		t.SocketBufferKB = 32
	}
	if t.UplinkChunkKB < 8 {
		t.UplinkChunkKB = 16
	}
	c.tuning = t
	c.log("[Client] Applied Tuning: Workers=%d, FastPoll=%dms, MaxIdle=%dms, Mux=%d",
		t.Workers, t.DownlinkFastPollMs, t.DownlinkMaxIdleMs, t.MuxConcurrency)
}

func (c *Client) Start() error {
	if c.cfg.ListenAddr == "" {
		c.cfg.ListenAddr = "127.0.0.1:10800"
	}

	urls := c.cfg.GetGasURLs()
	if len(urls) == 0 {
		return fmt.Errorf("at least one Google Apps Script URL is required")
	}

	pool, err := NewEndpointPool(urls, c.log)
	if err != nil {
		return err
	}
	c.pool = pool

	c.httpClient = buildHTTPClient(c.cfg.FrontDomain, c.cfg.FrontIP)

	l, err := net.Listen("tcp", c.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", c.cfg.ListenAddr, err)
	}
	c.listener = l
	c.running.Store(true)

	c.log("[Client] Listening for Xray on %s", c.cfg.ListenAddr)
	c.log("[Client] Active Google Script Pool: %d account(s)", len(urls))
	for _, ep := range c.pool.endpoints {
		c.log("[Client]  - %s: %s", ep.ShortName(), ep.URL)
	}
	if c.cfg.FrontDomain != "" {
		c.log("[Client] Domain Fronting SNI: %s (IP: %s)", c.cfg.FrontDomain, c.cfg.FrontIP)
	}

	// Start integrated Xray Core automatically if enabled
	if c.cfg.StartXray {
		_, portStr, err := net.SplitHostPort(c.cfg.ListenAddr)
		relayPort := 10800
		if err == nil {
			fmt.Sscanf(portStr, "%d", &relayPort)
		}

		c.xrayMgr = xray.NewManager(c.cfg.SocksPort, c.cfg.HttpPort, relayPort, c.GetTuning().MuxConcurrency, c.log)
		if err := c.xrayMgr.Start(c.GetTuning().MuxConcurrency); err != nil {
			c.log("[Client] Warning: could not start integrated Xray core: %v", err)
		}
	}

	go c.poolHealthMonitor()
	go c.keepWarmHeartbeat()

	go func() {
		for c.running.Load() {
			conn, err := c.listener.Accept()
			if err != nil {
				if !c.running.Load() {
					break
				}
				c.log("[Client] Accept error: %v", err)
				continue
			}
			go c.handleConnection(conn)
		}
	}()

	return nil
}

func (c *Client) Stop() {
	if c.running.CompareAndSwap(true, false) {
		if c.xrayMgr != nil {
			c.xrayMgr.Stop()
		}
		if c.listener != nil {
			_ = c.listener.Close()
		}
		c.log("[Client] Relay stopped.")
	}
}

func (c *Client) IsRunning() bool {
	return c.running.Load()
}

func (c *Client) GetPoolStatus() string {
	if c.pool == nil {
		return "Pool inactive"
	}
	return c.pool.GetStatusSummary()
}

func (c *Client) GetEndpointPool() *EndpointPool {
	return c.pool
}

func (c *Client) GetHTTPClient() *http.Client {
	return c.httpClient
}

func (c *Client) poolHealthMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for c.running.Load() {
		<-ticker.C
		if c.pool != nil {
			c.log("[Pool Status] %s", c.pool.GetStatusSummary())
		}
	}
}

func (c *Client) keepWarmHeartbeat() {
	ticker := time.NewTicker(100 * time.Second)
	defer ticker.Stop()

	for c.running.Load() {
		<-ticker.C
		if c.pool == nil {
			continue
		}
		for _, ep := range c.pool.endpoints {
			if ep.IsAvailable(time.Now().Unix()) {
				go func(target *Endpoint) {
					pingPkt := Packet{
						SessionID: "__ping__",
						Token:     c.cfg.Token,
					}
					_, _, _ = c.pool.SendPacket(c.httpClient, pingPkt, target)
				}(ep)
			}
		}
	}
}

func (c *Client) handleConnection(conn net.Conn) {
	tuning := c.GetTuning()

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
		bufSize := tuning.SocketBufferKB * 1024
		_ = tcp.SetReadBuffer(bufSize)
		_ = tcp.SetWriteBuffer(bufSize)
	}

	sessionID := generateSessionID()
	initialEP := c.pool.PickEndpoint()

	sess := &ClientSession{
		id:          sessionID,
		tcpConn:     conn,
		wakeDown:    make(chan struct{}, tuning.Workers),
		preferredEP: initialEP,
		nextSeq:     1,
		reorderBuf:  make(map[uint64][]byte),
	}
	defer sess.Close()

	c.log("[Client] New session %s via %s (Workers=%d)", sessionID, initialEP.ShortName(), tuning.Workers)

	// Launch concurrent downlink polling workers (sliding window)
	workers := tuning.Workers
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		go c.downlinkWorker(sess, i)
	}

	// Uplink Reader: Reads from local Xray TCP socket and posts data
	chunkSize := tuning.UplinkChunkKB * 1024
	if chunkSize < 8192 {
		chunkSize = 32768
	}
	buf := make([]byte, chunkSize)

	for !sess.closed.Load() && c.running.Load() {
		n, err := conn.Read(buf)
		if n > 0 {
			reqPacket := Packet{
				SessionID: sess.id,
				Token:     c.cfg.Token,
				Data:      base64.StdEncoding.EncodeToString(buf[:n]),
			}

			currentEP := sess.GetEndpoint()
			resp, usedEP, sendErr := c.pool.SendPacket(c.httpClient, reqPacket, currentEP)
			if usedEP != nil && usedEP != currentEP {
				sess.SetEndpoint(usedEP)
			}

			if sendErr == nil && resp != nil {
				if resp.Close {
					sess.Close()
					break
				}
				if resp.Data != "" {
					decoded, decErr := base64.StdEncoding.DecodeString(resp.Data)
					if decErr == nil && len(decoded) > 0 {
						if wErr := sess.WriteInOrder(resp.Seq, decoded); wErr != nil {
							sess.Close()
							break
						}
					}
				}
			}
			sess.TriggerWake()
		}

		if err != nil {
			_, _, _ = c.pool.SendPacket(c.httpClient, Packet{
				SessionID: sess.id,
				Token:     c.cfg.Token,
				Close:     true,
			}, sess.GetEndpoint())
			sess.Close()
			break
		}
	}
	c.log("[Client] Session %s closed", sessionID)
}

func (c *Client) downlinkWorker(sess *ClientSession, workerID int) {
	tuning := c.GetTuning()
	if workerID > 0 && tuning.WorkerStaggerMs > 0 {
		time.Sleep(time.Duration(workerID*tuning.WorkerStaggerMs) * time.Millisecond)
	}

	backoff := time.Duration(tuning.DownlinkFastPollMs) * time.Millisecond
	for !sess.closed.Load() && c.running.Load() {
		tuning = c.GetTuning()
		reqPacket := Packet{
			SessionID: sess.id,
			Token:     c.cfg.Token,
		}

		currentEP := sess.GetEndpoint()
		resp, usedEP, err := c.pool.SendPacket(c.httpClient, reqPacket, currentEP)
		if usedEP != nil && usedEP != currentEP {
			sess.SetEndpoint(usedEP)
		}

		if err != nil {
			time.Sleep(backoff)
			maxIdle := time.Duration(tuning.DownlinkMaxIdleMs) * time.Millisecond
			if backoff < maxIdle {
				backoff = time.Duration(float64(backoff)*1.5 + 10*float64(time.Millisecond))
			}
			continue
		}

		if resp.Close {
			sess.Close()
			break
		}

		if resp.Data != "" {
			decoded, decErr := base64.StdEncoding.DecodeString(resp.Data)
			if decErr == nil && len(decoded) > 0 {
				if writeErr := sess.WriteInOrder(resp.Seq, decoded); writeErr != nil {
					sess.Close()
					break
				}
			}
			backoff = time.Duration(tuning.DownlinkFastPollMs) * time.Millisecond
		} else {
			maxIdle := time.Duration(tuning.DownlinkMaxIdleMs) * time.Millisecond
			select {
			case <-sess.wakeDown:
				backoff = time.Duration(tuning.DownlinkFastPollMs) * time.Millisecond
			case <-time.After(backoff):
				if backoff < maxIdle {
					if backoff == 0 {
						backoff = 25 * time.Millisecond
					} else {
						backoff = time.Duration(float64(backoff) * 1.4)
					}
				}
			}
		}
	}
}

func buildHTTPClient(frontDomain, frontIP string) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	serverName := "script.google.com"
	if frontDomain != "" {
		serverName = frontDomain
	}

	tlsConfig := &tls.Config{
		ServerName:         serverName,
		NextProtos:         []string{"h2", "http/1.1"},
		InsecureSkipVerify: false,
	}

	transport := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 200,
		IdleConnTimeout:     120 * time.Second,
		DisableCompression:  true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			target := addr
			if frontIP != "" {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					port = "443"
				}
				target = net.JoinHostPort(frontIP, port)
			}
			return dialer.DialContext(ctx, network, target)
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   35 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

func generateSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
