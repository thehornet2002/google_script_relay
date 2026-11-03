package relay

import (
	"errors"
	"strings"
)

// Packet is sent between Client and Server through Google Apps Script.
type Packet struct {
	SessionID  string `json:"s"`
	RequestID  uint64 `json:"rid,omitempty"` // unique monotonic ID for uplink idempotency
	Token      string `json:"t,omitempty"`
	AdminToken string `json:"at,omitempty"`  // distinct authorization for remote tuning/admin
	SessionSig string `json:"sig,omitempty"` // HMAC session ownership signature
	Data       string `json:"d,omitempty"`   // base64 payload
	Close      bool   `json:"c,omitempty"`
}

// ResponsePacket is returned by the Server to the Client via Google Apps Script.
type ResponsePacket struct {
	Seq          uint64 `json:"seq,omitempty"` // incremental sequence number for ordering concurrent polls
	RequestID    uint64 `json:"rid,omitempty"` // echoed RequestID for confirmation
	Data         string `json:"d,omitempty"`   // base64 payload
	Close        bool   `json:"c,omitempty"`
	Error        string `json:"e,omitempty"`
	Timestamp    int64  `json:"timestamp,omitempty"`  // server timestamp in unix milliseconds
	ServerRxNano int64  `json:"rx_nano,omitempty"`    // precision nano receive time on server
	ServerTxNano int64  `json:"tx_nano,omitempty"`    // precision nano send time on server
	TotalSize    int    `json:"total_size,omitempty"` // for benchmark file streaming
	Offset       int    `json:"offset,omitempty"`     // offset in benchmark stream
	CRC32        uint32 `json:"crc32,omitempty"`      // checksum for data integrity validation
}

// AdminRequest represents remote management commands sent over the tunnel.
type AdminRequest struct {
	Action string        `json:"action"` // "get_tuning", "set_tuning", "save_config", "status"
	Tuning *ServerTuning `json:"tuning,omitempty"`
}

// AdminResponse represents server status and response for remote management.
type AdminResponse struct {
	Success  bool          `json:"success"`
	Message  string        `json:"message,omitempty"`
	Tuning   *ServerTuning `json:"tuning,omitempty"`
	XrayUUID string        `json:"xray_uuid,omitempty"`
	Uptime   int64         `json:"uptime,omitempty"`
}

// Config represents runtime settings for client or server.
type Config struct {
	Mode         string        `json:"mode"`                  // "client" or "server"
	ListenAddr   string        `json:"listen_addr"`           // client: 127.0.0.1:10800, server: 0.0.0.0:8080
	TargetAddr   string        `json:"target_addr"`           // server: 127.0.0.1:10001 (VPS Xray inbound)
	GasURL       string        `json:"gas_url,omitempty"`     // single URL (backward compatibility)
	GasURLs      []string      `json:"gas_urls,omitempty"`    // multiple Google Script URLs for load balancing
	Token        string        `json:"token"`                 // shared data authorization token
	AdminToken   string        `json:"admin_token,omitempty"` // separate administrative token for remote management
	XrayUUID     string        `json:"xray_uuid,omitempty"`   // random per-installation UUID for Xray
	FrontDomain  string        `json:"front_domain"`          // optional: TLS SNI for domain fronting (e.g. www.google.com)
	FrontIP      string        `json:"front_ip"`              // optional: Clean Google IP for domain fronting
	ClientTuning *ClientTuning `json:"client_tuning,omitempty"`
	ServerTuning *ServerTuning `json:"server_tuning,omitempty"`
	SocksPort    int           `json:"socks_port,omitempty"` // integrated Xray SOCKS5 port (default: 10808)
	HttpPort     int           `json:"http_port,omitempty"`  // integrated Xray HTTP port (default: 10809)
	StartXray    bool          `json:"start_xray,omitempty"` // automatically start integrated Xray
}

// GetGasURLs returns a sanitized, deduplicated list of configured Google Script URLs.
func (c *Config) GetGasURLs() []string {
	var list []string
	seen := make(map[string]bool)

	for _, u := range c.GasURLs {
		trimmed := strings.TrimSpace(u)
		if trimmed != "" && !seen[trimmed] {
			list = append(list, trimmed)
			seen[trimmed] = true
		}
	}
	if c.GasURL != "" {
		trimmed := strings.TrimSpace(c.GasURL)
		if trimmed != "" && !seen[trimmed] {
			list = append(list, trimmed)
			seen[trimmed] = true
		}
	}
	return list
}

func (c *Config) EnsureDefaults() {
	if c.ClientTuning == nil {
		def := DefaultClientTuning()
		c.ClientTuning = &def
	} else {
		if c.ClientTuning.Workers < 1 || c.ClientTuning.Workers > 4 {
			c.ClientTuning.Workers = 2
		}
		if c.ClientTuning.WorkerStaggerMs < 0 || c.ClientTuning.WorkerStaggerMs > 500 {
			c.ClientTuning.WorkerStaggerMs = 250
		}
		if c.ClientTuning.DownlinkFastPollMs < 0 || c.ClientTuning.DownlinkFastPollMs > 50 {
			c.ClientTuning.DownlinkFastPollMs = 5
		}
		if c.ClientTuning.DownlinkMaxIdleMs < 100 || c.ClientTuning.DownlinkMaxIdleMs > 2000 {
			c.ClientTuning.DownlinkMaxIdleMs = 600
		}
		if c.ClientTuning.MuxConcurrency < 0 || c.ClientTuning.MuxConcurrency > 32 {
			c.ClientTuning.MuxConcurrency = 8
		}
		if c.ClientTuning.SocketBufferKB < 16 || c.ClientTuning.SocketBufferKB > 512 {
			c.ClientTuning.SocketBufferKB = 64
		}
		if c.ClientTuning.UplinkChunkKB < 8 || c.ClientTuning.UplinkChunkKB > 256 {
			c.ClientTuning.UplinkChunkKB = 32
		}
	}
	if c.ServerTuning == nil {
		def := DefaultServerTuning()
		c.ServerTuning = &def
	} else {
		if c.ServerTuning.WaitTimeoutMs < 100 || c.ServerTuning.WaitTimeoutMs > 2000 {
			c.ServerTuning.WaitTimeoutMs = 800
		}
		if c.ServerTuning.MaxDrainBatchBytes < 8192 || c.ServerTuning.MaxDrainBatchBytes > 524288 {
			c.ServerTuning.MaxDrainBatchBytes = 131072
		}
		if c.ServerTuning.DownlinkQueueCap < 64 || c.ServerTuning.DownlinkQueueCap > 1024 {
			c.ServerTuning.DownlinkQueueCap = 256
		}
	}
	if c.SocksPort == 0 {
		c.SocksPort = 10808
	}
	if c.HttpPort == 0 {
		c.HttpPort = 10809
	}
	if c.XrayUUID == "" {
		c.XrayUUID = "e2a4a9c8-7c8b-4a5e-9e2a-1a2b3c4d5e6f"
	}
	if c.AdminToken == "" && c.Token != "" {
		c.AdminToken = c.Token
	}
}

// Validate checks configuration sanity and enforces mandatory security rules.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Token) == "" {
		return errors.New("data token must not be empty; please configure a secure token")
	}
	if len(strings.TrimSpace(c.Token)) < 8 {
		return errors.New("token length is too short; minimum 8 characters required")
	}
	if c.Mode == "client" {
		if len(c.GetGasURLs()) == 0 {
			return errors.New("at least one Google Apps Script Web App URL is required in client mode")
		}
		if c.ClientTuning != nil {
			if err := c.ClientTuning.Validate(); err != nil {
				return err
			}
		}
	}
	if c.Mode == "server" {
		if c.ServerTuning != nil {
			if err := c.ServerTuning.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}
