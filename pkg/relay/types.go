package relay

import "strings"

// Packet is sent between Client and Server through Google Apps Script.
type Packet struct {
	SessionID string `json:"s"`
	Token     string `json:"t,omitempty"`
	Data      string `json:"d,omitempty"` // base64 payload
	Close     bool   `json:"c,omitempty"`
}

// ResponsePacket is returned by the Server to the Client via Google Apps Script.
type ResponsePacket struct {
	Seq          uint64 `json:"seq,omitempty"`            // incremental sequence number for ordering concurrent polls
	Data         string `json:"d,omitempty"`              // base64 payload
	Close        bool   `json:"c,omitempty"`
	Error        string `json:"e,omitempty"`
	Timestamp    int64  `json:"timestamp,omitempty"`      // server timestamp in unix milliseconds
	ServerRxNano int64  `json:"rx_nano,omitempty"`        // precision nano receive time on server
	ServerTxNano int64  `json:"tx_nano,omitempty"`        // precision nano send time on server
	TotalSize    int    `json:"total_size,omitempty"`     // for benchmark file streaming
	Offset       int    `json:"offset,omitempty"`         // offset in benchmark stream
	CRC32        uint32 `json:"crc32,omitempty"`          // checksum for data integrity validation
}

// AdminRequest represents remote management commands sent over the tunnel.
type AdminRequest struct {
	Action string        `json:"action"` // "get_tuning", "set_tuning", "save_config", "status"
	Tuning *ServerTuning `json:"tuning,omitempty"`
}

// AdminResponse represents server status and response for remote management.
type AdminResponse struct {
	Success bool          `json:"success"`
	Message string        `json:"message,omitempty"`
	Tuning  *ServerTuning `json:"tuning,omitempty"`
	Uptime  int64         `json:"uptime,omitempty"`
}

// Config represents runtime settings for client or server.
type Config struct {
	Mode         string        `json:"mode"`                  // "client" or "server"
	ListenAddr   string        `json:"listen_addr"`           // client: 127.0.0.1:10800, server: 0.0.0.0:8080
	TargetAddr   string        `json:"target_addr"`           // server: 127.0.0.1:10001 (VPS Xray inbound)
	GasURL       string        `json:"gas_url,omitempty"`    // single URL (backward compatibility)
	GasURLs      []string      `json:"gas_urls,omitempty"`   // multiple Google Script URLs for load balancing
	Token        string        `json:"token"`                 // shared authentication token
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
	}
	if c.ServerTuning == nil {
		def := DefaultServerTuning()
		c.ServerTuning = &def
	}
	if c.SocksPort == 0 {
		c.SocksPort = 10808
	}
	if c.HttpPort == 0 {
		c.HttpPort = 10809
	}
}
