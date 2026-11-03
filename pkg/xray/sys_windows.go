//go:build windows

package xray

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

var (
	wininet                          = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOptionW           = wininet.NewProc("InternetSetOptionW")
	INTERNET_OPTION_SETTINGS_CHANGED = uintptr(39)
	INTERNET_OPTION_REFRESH          = uintptr(37)
)

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

func terminateProcessGracefully(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	// Try taskkill gracefully first
	cmd := exec.Command("taskkill", "/PID", string(rune(proc.Pid)), "/T")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	_ = cmd.Run()

	done := make(chan error, 1)
	go func() {
		state, err := proc.Wait()
		_ = state
		done <- err
	}()

	select {
	case <-done:
		return nil
	case <-time.After(1500 * time.Millisecond):
		return proc.Kill()
	}
}

// SetSystemProxy configures or clears the Windows System Proxy (wininet)
func SetSystemProxy(enable bool, httpProxyAddr string) error {
	if enable {
		if httpProxyAddr == "" {
			httpProxyAddr = "127.0.0.1:10809"
		}
		cmd1 := exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
		cmd1.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = cmd1.Run()

		cmd2 := exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`, "/v", "ProxyServer", "/t", "REG_SZ", "/d", httpProxyAddr, "/f")
		cmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = cmd2.Run()
	} else {
		cmd1 := exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
		cmd1.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = cmd1.Run()
	}

	procInternetSetOptionW.Call(0, INTERNET_OPTION_SETTINGS_CHANGED, 0, 0)
	procInternetSetOptionW.Call(0, INTERNET_OPTION_REFRESH, 0, 0)
	return nil
}
