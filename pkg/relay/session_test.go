package relay

import (
	"bytes"
	"net"
	"testing"
)

type mockConn struct {
	net.Conn
	buf bytes.Buffer
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return m.buf.Write(b)
}

func (m *mockConn) Close() error {
	return nil
}

func TestClientSessionReorderBuffer(t *testing.T) {
	mock := &mockConn{}
	sess := &ClientSession{
		id:         "test-sess-001",
		tcpConn:    mock,
		nextSeq:    1,
		reorderBuf: make(map[uint64][]byte),
	}

	// Receive packet 2 before packet 1 (out of order)
	if err := sess.WriteInOrder(2, []byte("World!")); err != nil {
		t.Fatalf("unexpected error on seq 2: %v", err)
	}

	// Nothing should be written yet because packet 1 has not arrived
	if mock.buf.Len() != 0 {
		t.Fatalf("expected 0 bytes written, got %d", mock.buf.Len())
	}

	// Now receive packet 1
	if err := sess.WriteInOrder(1, []byte("Hello ")); err != nil {
		t.Fatalf("unexpected error on seq 1: %v", err)
	}

	// Both should now be drained sequentially
	expected := "Hello World!"
	if mock.buf.String() != expected {
		t.Fatalf("expected %q, got %q", expected, mock.buf.String())
	}
}

func TestClientSessionReorderBufferOverflowProtection(t *testing.T) {
	mock := &mockConn{}
	sess := &ClientSession{
		id:         "test-sess-overflow",
		tcpConn:    mock,
		nextSeq:    1,
		reorderBuf: make(map[uint64][]byte),
	}

	// Try to flood with out-of-order packets exceeding MaxReorderPackets
	var overflowErr error
	for i := uint64(2); i <= MaxReorderPackets+10; i++ {
		err := sess.WriteInOrder(i, []byte("chunk"))
		if err != nil {
			overflowErr = err
			break
		}
	}

	if overflowErr == nil {
		t.Fatalf("expected error when exceeding MaxReorderPackets capacity")
	}
	if !sess.closed.Load() {
		t.Fatalf("session should be closed after capacity overflow")
	}
}

func TestServerSessionDeduplication(t *testing.T) {
	sess := &ServerSession{
		id: "test-server-dedup",
	}

	// First time RequestID 101 arrives
	if sess.MarkAndCheckProcessed(101) {
		t.Fatalf("first occurrence of RequestID should not be marked as duplicate")
	}

	// Second time RequestID 101 arrives (simulated retry)
	if !sess.MarkAndCheckProcessed(101) {
		t.Fatalf("second occurrence of RequestID 101 must be flagged as duplicate")
	}

	// Distinct RequestID 102
	if sess.MarkAndCheckProcessed(102) {
		t.Fatalf("first occurrence of RequestID 102 should not be duplicate")
	}

	// RequestID 0 is ignored for deduplication
	if sess.MarkAndCheckProcessed(0) {
		t.Fatalf("RequestID 0 should not be marked as processed")
	}
}
