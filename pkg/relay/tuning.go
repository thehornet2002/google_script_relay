package relay

// ClientTuning holds low-level performance dials for the client relay engine.
type ClientTuning struct {
	Workers            int `json:"workers"`               // Concurrent downlink workers (1 - 4)
	WorkerStaggerMs    int `json:"worker_stagger_ms"`     // Stagger offset in ms between worker polls (0 - 400)
	DownlinkFastPollMs int `json:"downlink_fast_poll_ms"` // Poll interval when data is actively streaming (0 - 30)
	DownlinkMaxIdleMs  int `json:"downlink_max_idle_ms"`  // Max backoff sleep when idle to save quota (200 - 1500)
	MuxConcurrency     int `json:"mux_concurrency"`       // Xray Mux concurrency (0 = disabled, 4, 8, 16)
	SocketBufferKB     int `json:"socket_buffer_kb"`      // TCP socket buffer in KB (32, 64, 128)
	UplinkChunkKB      int `json:"uplink_chunk_kb"`       // Read buffer from local Xray in KB (16, 32, 64)
}

// ServerTuning holds low-level performance dials for the VPS server relay engine.
type ServerTuning struct {
	WaitTimeoutMs      int `json:"wait_timeout_ms"`       // VPS long-poll hold timeout in ms (200 - 1500)
	MaxDrainBatchBytes int `json:"max_drain_batch_bytes"` // Max bytes drained per HTTP response (32768 - 262144)
	DownlinkQueueCap   int `json:"downlink_queue_cap"`    // Downlink channel capacity (128 - 512)
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
