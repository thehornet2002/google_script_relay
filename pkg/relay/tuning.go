package relay

import "fmt"

// ClientTuning holds low-level performance dials for the client relay engine.
type ClientTuning struct {
	Workers            int `json:"workers"`               // Concurrent downlink workers (1 - 4)
	WorkerStaggerMs    int `json:"worker_stagger_ms"`     // Stagger offset in ms between worker polls (0 - 500)
	DownlinkFastPollMs int `json:"downlink_fast_poll_ms"` // Poll interval when data is actively streaming (0 - 50)
	DownlinkMaxIdleMs  int `json:"downlink_max_idle_ms"`  // Max backoff sleep when idle to save quota (100 - 2000)
	MuxConcurrency     int `json:"mux_concurrency"`       // Xray Mux concurrency (0 = disabled, 1 - 32)
	SocketBufferKB     int `json:"socket_buffer_kb"`      // TCP socket buffer in KB (16 - 512)
	UplinkChunkKB      int `json:"uplink_chunk_kb"`       // Read buffer from local Xray in KB (8 - 256)
}

func (t ClientTuning) Validate() error {
	if t.Workers < 1 || t.Workers > 4 {
		return fmt.Errorf("invalid workers count %d: must be between 1 and 4", t.Workers)
	}
	if t.WorkerStaggerMs < 0 || t.WorkerStaggerMs > 500 {
		return fmt.Errorf("invalid worker stagger %d ms: must be between 0 and 500 ms", t.WorkerStaggerMs)
	}
	if t.DownlinkFastPollMs < 0 || t.DownlinkFastPollMs > 50 {
		return fmt.Errorf("invalid fast poll %d ms: must be between 0 and 50 ms", t.DownlinkFastPollMs)
	}
	if t.DownlinkMaxIdleMs < 100 || t.DownlinkMaxIdleMs > 2000 {
		return fmt.Errorf("invalid max idle backoff %d ms: must be between 100 and 2000 ms", t.DownlinkMaxIdleMs)
	}
	if t.MuxConcurrency < 0 || t.MuxConcurrency > 32 {
		return fmt.Errorf("invalid mux concurrency %d: must be between 0 and 32", t.MuxConcurrency)
	}
	if t.SocketBufferKB < 16 || t.SocketBufferKB > 512 {
		return fmt.Errorf("invalid socket buffer %d KB: must be between 16 and 512 KB", t.SocketBufferKB)
	}
	if t.UplinkChunkKB < 8 || t.UplinkChunkKB > 256 {
		return fmt.Errorf("invalid uplink chunk %d KB: must be between 8 and 256 KB", t.UplinkChunkKB)
	}
	return nil
}

// ServerTuning holds low-level performance dials for the VPS server relay engine.
type ServerTuning struct {
	WaitTimeoutMs      int `json:"wait_timeout_ms"`       // VPS long-poll hold timeout in ms (100 - 2000)
	MaxDrainBatchBytes int `json:"max_drain_batch_bytes"` // Max bytes drained per HTTP response (8192 - 524288)
	DownlinkQueueCap   int `json:"downlink_queue_cap"`    // Downlink channel capacity (64 - 1024)
}

func (t ServerTuning) Validate() error {
	if t.WaitTimeoutMs < 100 || t.WaitTimeoutMs > 2000 {
		return fmt.Errorf("invalid wait timeout %d ms: must be between 100 and 2000 ms", t.WaitTimeoutMs)
	}
	if t.MaxDrainBatchBytes < 8192 || t.MaxDrainBatchBytes > 524288 {
		return fmt.Errorf("invalid max drain batch %d bytes: must be between 8192 and 524288 bytes", t.MaxDrainBatchBytes)
	}
	if t.DownlinkQueueCap < 64 || t.DownlinkQueueCap > 1024 {
		return fmt.Errorf("invalid downlink queue capacity %d: must be between 64 and 1024", t.DownlinkQueueCap)
	}
	return nil
}

// DefaultClientTuning returns optimal default client parameters.
func DefaultClientTuning() ClientTuning {
	return ClientTuning{
		Workers:            2,
		WorkerStaggerMs:    250,
		DownlinkFastPollMs: 5,
		DownlinkMaxIdleMs:  600,
		MuxConcurrency:     8,
		SocketBufferKB:     64,
		UplinkChunkKB:      32,
	}
}

// DefaultServerTuning returns optimal default server parameters.
func DefaultServerTuning() ServerTuning {
	return ServerTuning{
		WaitTimeoutMs:      800,
		MaxDrainBatchBytes: 131072, // 128 KB
		DownlinkQueueCap:   256,
	}
}

// Preset profiles
func ProfilePingClientTuning() ClientTuning {
	return ClientTuning{
		Workers:            2,
		WorkerStaggerMs:    150,
		DownlinkFastPollMs: 0,
		DownlinkMaxIdleMs:  300,
		MuxConcurrency:     0, // 0 = disabled for lowest individual stream latency
		SocketBufferKB:     32,
		UplinkChunkKB:      16,
	}
}

func ProfilePingServerTuning() ServerTuning {
	return ServerTuning{
		WaitTimeoutMs:      400,
		MaxDrainBatchBytes: 65536,
		DownlinkQueueCap:   128,
	}
}

func ProfileSpeedClientTuning() ClientTuning {
	return ClientTuning{
		Workers:            3,
		WorkerStaggerMs:    200,
		DownlinkFastPollMs: 0,
		DownlinkMaxIdleMs:  800,
		MuxConcurrency:     16,
		SocketBufferKB:     128,
		UplinkChunkKB:      64,
	}
}

func ProfileSpeedServerTuning() ServerTuning {
	return ServerTuning{
		WaitTimeoutMs:      1000,
		MaxDrainBatchBytes: 262144, // 256 KB
		DownlinkQueueCap:   512,
	}
}

func ProfileBalancedClientTuning() ClientTuning {
	return DefaultClientTuning()
}

func ProfileBalancedServerTuning() ServerTuning {
	return DefaultServerTuning()
}
