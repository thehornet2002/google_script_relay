//go:build !windows

package xray

import (
	"os/exec"
)

func setProcAttr(cmd *exec.Cmd) {
	// No-op on non-windows
}

func SetSystemProxy(enable bool, httpProxyAddr string) error {
	// No-op on non-windows
	return nil
}
