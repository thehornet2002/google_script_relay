//go:build !windows

package xray

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func setProcAttr(cmd *exec.Cmd) {
	// No-op on non-windows
}

func terminateProcessGracefully(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	_ = proc.Signal(syscall.SIGTERM)

	done := make(chan error, 1)
	go func() {
		state, err := proc.Wait()
		_ = state
		done <- err
	}()

	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return proc.Kill()
	}
}

func SetSystemProxy(enable bool, httpProxyAddr string) error {
	// No-op on non-windows
	return nil
}
