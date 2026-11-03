package relay

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"testing"
)

func FuzzPacketUnmarshal(f *testing.F) {
	// Seed corpus
	f.Add([]byte(`{"s":"sess-1","t":"tok-123","d":"SGVsbG8=","c":false}`))
	f.Add([]byte(`{"s":"","t":"","d":"","c":true}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`invalid-json-data`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var p Packet
		if err := json.Unmarshal(data, &p); err == nil {
			// If JSON unmarshal succeeded, test base64 decoding safety
			if p.Data != "" {
				_, _ = base64.StdEncoding.DecodeString(p.Data)
			}
		}
	})
}

func FuzzBenchmarkOffsetParser(f *testing.F) {
	f.Add("0")
	f.Add("1024")
	f.Add("-1")
	f.Add("99999999999999999999")
	f.Add("not-a-number")

	all, _ := getBenchmarkPayload()

	f.Fuzz(func(t *testing.T, input string) {
		offset, err := strconv.Atoi(input)
		if err == nil {
			// Verify bounds checking behavior
			if offset < 0 || offset > len(all) {
				// Should be caught and rejected by bounds check
				return
			}
			chunkSize := 131072
			end := offset + chunkSize
			if end > len(all) {
				end = len(all)
			}
			_ = all[offset:end]
		}
	})
}
