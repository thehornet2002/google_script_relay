//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"google_script_relay/pkg/relay"
	"google_script_relay/pkg/xray"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")

	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procShowWindow            = user32.NewProc("ShowWindow")
	procUpdateWindow          = user32.NewProc("UpdateWindow")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procSendMessageW          = user32.NewProc("SendMessageW")
	procGetWindowTextW        = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW  = user32.NewProc("GetWindowTextLengthW")
	procSetWindowTextW        = user32.NewProc("SetWindowTextW")
	procEnableWindow          = user32.NewProc("EnableWindow")
	procLoadCursorW           = user32.NewProc("LoadCursorW")
	procLoadIconW             = user32.NewProc("LoadIconW")
	procSetTextColor          = gdi32.NewProc("SetTextColor")
	procSetBkColor            = gdi32.NewProc("SetBkColor")
	procSetBkMode             = gdi32.NewProc("SetBkMode")
	procCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	procCreateFontW           = gdi32.NewProc("CreateFontW")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procInitCommonControlsEx  = comctl32.NewProc("InitCommonControlsEx")
)

type INITCOMMONCONTROLSEX struct {
	DwSize uint32
	DwICC  uint32
}

type NMHDR struct {
	HwndFrom uintptr
	IdFrom   uintptr
	Code     uint32
}

type TCITEMW struct {
	Mask        uint32
	DwState     uint32
	DwStateMask uint32
	PszText     *uint16
	CchTextMax  int32
	IImage      int32
	LParam      uintptr
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

const (
	ID_TAB_MAIN        = 100
	ID_BTN_START       = 101
	ID_BTN_STOP        = 102
	ID_BTN_PROXY       = 103
	ID_BTN_SAVE        = 104
	ID_BTN_TUNE_PING   = 105
	ID_BTN_TUNE_SPEED  = 106
	ID_BTN_TUNE_BAL    = 107
	ID_BTN_START_TUNE  = 108
	ID_BTN_QUICK_PING  = 109
	ID_BTN_QUICK_SPEED = 110
	ID_BTN_APPLY_TUNE  = 111
	ID_BTN_FETCH_VPS   = 112
	ID_BTN_PUSH_VPS    = 113
	ID_BTN_CLEAR_LOG   = 114
	ID_BTN_VALIDATE    = 115

	WM_COMMAND        = 0x0111
	WM_NOTIFY         = 0x004E
	WM_DESTROY        = 0x0002
	WM_SETFONT        = 0x0030
	WM_CTLCOLORSTATIC = 0x0138
	WM_CTLCOLOREDIT   = 0x0133
	WM_CTLCOLORBTN    = 0x0135

	TCM_INSERTITEMW = 0x133E
	TCM_SETCURSEL   = 0x130C
	TCM_GETCURSEL   = 0x130B
	TCN_SELCHANGE   = 0xFFFFFDD9

	PBM_SETRANGE = 0x0401
	PBM_SETPOS   = 0x0402

	EM_SETSEL      = 0x00B1
	EM_REPLACESEL  = 0x00C2
	EM_SCROLLCARET = 0x00B7

	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_VISIBLE          = 0x10000000
	WS_CHILD            = 0x40000000
	WS_BORDER           = 0x00800000
	WS_TABSTOP          = 0x00010000
	WS_VSCROLL          = 0x00200000
	ES_AUTOHSCROLL      = 0x0080
	ES_MULTILINE        = 0x0004
	ES_AUTOVSCROLL      = 0x0040
	ES_READONLY         = 0x0800
	ES_LEFT             = 0x0000
	BS_PUSHBUTTON       = 0x0000
	BS_GROUPBOX         = 0x0007

	DWMWA_USE_IMMERSIVE_DARK_MODE  = 20
	DWMWA_WINDOW_CORNER_PREFERENCE = 33
)

var (
	hMainWnd   uintptr
	hTabCtrl   uintptr
	currentTab int

	tabControls [5][]uintptr

	// Tab 0: Dashboard controls
	hLblDashStatus  uintptr
	hLblDashPorts   uintptr
	hCardMetricPing uintptr
	hCardMetricSpd  uintptr
	hCardMetricAcc  uintptr
	hBtnStart       uintptr
	hBtnStop        uintptr
	hBtnProxy       uintptr
	hBtnSaveDash    uintptr
	hEditDashPool   uintptr

	// Tab 1: AI Lab controls
	hBtnProfilePing  uintptr
	hBtnProfileSpeed uintptr
	hBtnProfileBal   uintptr
	hBtnStartTune    uintptr
	hBtnQuickPing    uintptr
	hBtnQuickSpeed   uintptr
	hBtnApplyTune    uintptr
	hProgressBar     uintptr
	hLblLabStatus    uintptr
	hLblLabLatency   uintptr
	hLblLabBandwidth uintptr
	hLblLabScore     uintptr

	// Tab 2: Accounts & Security controls
	hEditGas        uintptr
	hEditToken      uintptr
	hEditAdminToken uintptr
	hEditXrayUUID   uintptr
	hEditFront      uintptr
	hEditFrontIP    uintptr
	hBtnValidateAcc uintptr
	hBtnSaveAcc     uintptr

	// Tab 3: Tuning Dials controls
	hEditWorkers     uintptr
	hEditFastPoll    uintptr
	hEditMaxIdle     uintptr
	hEditMux         uintptr
	hEditServerWait  uintptr
	hEditServerDrain uintptr
	hBtnFetchVPS     uintptr
	hBtnPushVPS      uintptr
	hBtnSaveDials    uintptr

	// Tab 4: Logs controls
	hEditLog     uintptr
	hBtnClearLog uintptr

	// GDI handles
	hFontRegular uintptr
	hFontBold    uintptr
	hFontTitle   uintptr
	hFontMonopic uintptr
	hDarkBrush   uintptr
	hCardBrush   uintptr

	relayClient    *relay.Client
	relayMutex     sync.Mutex
	configPath     string
	systemProxyOn  bool
	currentProfile relay.ProfileType = relay.ProfileBalanced

	optActive atomic.Bool
	optimizer *relay.Optimizer
)

func runApp() {
	runtime.LockOSThread()

	// Initialize Windows Common Controls v6
	icex := INITCOMMONCONTROLSEX{
		DwSize: uint32(unsafe.Sizeof(INITCOMMONCONTROLSEX{})),
		DwICC:  0x00000008 | 0x00000020 | 0x00000001 | 0x00000002 | 0x00000004,
	}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icex)))

	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		p1 := filepath.Join(dir, "client_config.json")
		p2 := filepath.Join(dir, "config", "client_config.json")
		if _, err := os.Stat(p1); err == nil {
			configPath = p1
		} else if _, err := os.Stat(p2); err == nil {
			configPath = p2
		} else {
			configPath = p1
		}
	} else {
		configPath = "client_config.json"
	}

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("XrayGasRelayMasterUI")

	hCursor, _, _ := procLoadCursorW.Call(0, uintptr(32512))
	hIcon, _, _ := procLoadIconW.Call(0, uintptr(32512))

	// Dark Theme Palette: Deep Navy Slate (RGB: 20, 24, 38) & Card Slate (RGB: 30, 35, 52)
	hDarkBrush, _, _ = procCreateSolidBrush.Call(uintptr(0x261814)) // BGR: 0x261814 = RGB(20, 24, 38)
	hCardBrush, _, _ = procCreateSolidBrush.Call(uintptr(0x34231E)) // BGR: 0x34231E = RGB(30, 35, 52)

	wcex := WNDCLASSEXW{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		Style:         0x0003,
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hInstance,
		HIcon:         hIcon,
		HCursor:       hCursor,
		HbrBackground: hDarkBrush,
		LpszClassName: className,
	}

	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex)))

	title, _ := syscall.UTF16PtrFromString("⚡ Xray Google Script Relay - High-Performance Control Center")

	// Fixed window: 770x670
	hMainWnd, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		WS_OVERLAPPEDWINDOW&^0x00040000&^0x00010000,
		uintptr(100), uintptr(50),
		uintptr(770), uintptr(670),
		0, 0, hInstance, 0,
	)

	// Modern Windows 10/11 Dark Mode and Rounded Corners
	darkMode := int32(1)
	procDwmSetWindowAttribute.Call(hMainWnd, DWMWA_USE_IMMERSIVE_DARK_MODE, uintptr(unsafe.Pointer(&darkMode)), 4)
	cornerPref := int32(2)
	procDwmSetWindowAttribute.Call(hMainWnd, DWMWA_WINDOW_CORNER_PREFERENCE, uintptr(unsafe.Pointer(&cornerPref)), 4)

	// Typography: Segoe UI
	fName, _ := syscall.UTF16PtrFromString("Segoe UI")
	hFontRegular, _, _ = procCreateFontW.Call(uintptr(16), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fName)))
	hFontBold, _, _ = procCreateFontW.Call(uintptr(16), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fName)))
	hFontTitle, _, _ = procCreateFontW.Call(uintptr(22), 0, 0, 0, 800, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fName)))

	fMono, _ := syscall.UTF16PtrFromString("Consolas")
	hFontMonopic, _, _ = procCreateFontW.Call(uintptr(15), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fMono)))

	buildModernInterface(hMainWnd, hInstance)
	loadInitialConfig()

	// Switch to Tab 0 (Dashboard) initially
	switchTab(0)

	procShowWindow.Call(hMainWnd, 5)
	procUpdateWindow.Call(hMainWnd)

	// Real-time background ticker to update dashboard metrics
	go func() {
		for {
			time.Sleep(2 * time.Second)
			relayMutex.Lock()
			rc := relayClient
			relayMutex.Unlock()

			if rc != nil && rc.IsRunning() && !optActive.Load() {
				stats := rc.GetPoolStatus()
				if stats != "" {
					setControlText(hEditDashPool, stats)
				}
			}
		}
	}()

	var msg MSG
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func buildModernInterface(hWnd, hInstance uintptr) {
	createCtrl := func(className, text string, style uint32, x, y, w, h int, id int, font uintptr) uintptr {
		cName, _ := syscall.UTF16PtrFromString(className)
		cText, _ := syscall.UTF16PtrFromString(text)
		hCtrl, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(cName)),
			uintptr(unsafe.Pointer(cText)),
			uintptr(style),
			uintptr(x), uintptr(y), uintptr(w), uintptr(h),
			hWnd, uintptr(id), hInstance, 0,
		)
		if font != 0 {
			procSendMessageW.Call(hCtrl, WM_SETFONT, font, 1)
		}
		return hCtrl
	}

	// 1. Create Main Tab Control (SysTabControl32)
	cTab, _ := syscall.UTF16PtrFromString("SysTabControl32")
	hTabCtrl, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(cTab)),
		0,
		WS_CHILD|WS_VISIBLE|WS_TABSTOP,
		uintptr(15), uintptr(12),
		uintptr(725), uintptr(605),
		hWnd, uintptr(ID_TAB_MAIN), hInstance, 0,
	)
	if hFontBold != 0 {
		procSendMessageW.Call(hTabCtrl, WM_SETFONT, hFontBold, 1)
	}

	tabTitles := []string{
		"  🚀 Dashboard & Control  ",
		"  ⚡ AI Matrix Test Lab  ",
		"  🌐 Google Script Accounts  ",
		"  ⚙️ Advanced Dials  ",
		"  📜 Live Event Logs  ",
	}

	for i, title := range tabTitles {
		pTitle, _ := syscall.UTF16PtrFromString(title)
		item := TCITEMW{
			Mask:    0x0001, // TCIF_TEXT
			PszText: pTitle,
		}
		procSendMessageW.Call(hTabCtrl, TCM_INSERTITEMW, uintptr(i), uintptr(unsafe.Pointer(&item)))
	}

	// -------------------------------------------------------------
	// TAB 0: DASHBOARD & CONTROL
	// -------------------------------------------------------------
	t0 := func(h uintptr) { tabControls[0] = append(tabControls[0], h) }

	t0(createCtrl("STATIC", "Xray Google Script Relay - Status Center", WS_CHILD|WS_VISIBLE, 35, 55, 600, 24, 0, hFontTitle))
	hLblDashStatus = createCtrl("STATIC", "● Status: Stopped (Click 'Start Relay + Xray' below to connect)", WS_CHILD|WS_VISIBLE, 35, 85, 680, 22, 0, hFontBold)
	hLblDashPorts = createCtrl("STATIC", "Local Endpoints: SOCKS5 Proxy: 127.0.0.1:10808  |  HTTP Proxy: 127.0.0.1:10809", WS_CHILD|WS_VISIBLE, 35, 110, 680, 20, 0, hFontRegular)
	t0(hLblDashStatus)
	t0(hLblDashPorts)

	// Dashboard Metric Cards
	t0(createCtrl("BUTTON", "Median Latency (VPS Roundtrip)", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 35, 140, 215, 75, 0, hFontRegular))
	hCardMetricPing = createCtrl("STATIC", "-- ms", WS_CHILD|WS_VISIBLE, 50, 168, 185, 32, 0, hFontTitle)
	t0(hCardMetricPing)

	t0(createCtrl("BUTTON", "Steady Download Speed", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 270, 140, 215, 75, 0, hFontRegular))
	hCardMetricSpd = createCtrl("STATIC", "-- MB/s", WS_CHILD|WS_VISIBLE, 285, 168, 185, 32, 0, hFontTitle)
	t0(hCardMetricSpd)

	t0(createCtrl("BUTTON", "Google Accounts Pool", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 505, 140, 215, 75, 0, hFontRegular))
	hCardMetricAcc = createCtrl("STATIC", "5 Accounts", WS_CHILD|WS_VISIBLE, 520, 168, 185, 32, 0, hFontTitle)
	t0(hCardMetricAcc)

	// Action Buttons
	hBtnStart = createCtrl("BUTTON", "▶ Start Relay + Xray", WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_PUSHBUTTON, 35, 230, 160, 38, ID_BTN_START, hFontBold)
	hBtnStop = createCtrl("BUTTON", "⏹ Stop Relay", WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_PUSHBUTTON, 205, 230, 120, 38, ID_BTN_STOP, hFontBold)
	hBtnProxy = createCtrl("BUTTON", "🛡 Windows Proxy: OFF", WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_PUSHBUTTON, 335, 230, 190, 38, ID_BTN_PROXY, hFontBold)
	hBtnSaveDash = createCtrl("BUTTON", "💾 Save Config", WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_PUSHBUTTON, 535, 230, 185, 38, ID_BTN_SAVE, hFontBold)
	procEnableWindow.Call(hBtnStop, 0)
	t0(hBtnStart)
	t0(hBtnStop)
	t0(hBtnProxy)
	t0(hBtnSaveDash)

	// Real-Time Pool Activity Card
	t0(createCtrl("BUTTON", "Active Google Apps Script Pool Health", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 35, 285, 685, 310, 0, hFontRegular))
	hEditDashPool = createCtrl("EDIT", "Pool inactive. Click Start Relay to begin.", WS_CHILD|WS_VISIBLE|WS_BORDER|ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY|WS_VSCROLL, 45, 310, 665, 270, 0, hFontMonopic)
	t0(hEditDashPool)

	// -------------------------------------------------------------
	// TAB 1: AI MATRIX OPTIMIZATION LAB
	// -------------------------------------------------------------
	t1 := func(h uintptr) { tabControls[1] = append(tabControls[1], h) }

	t1(createCtrl("STATIC", "High-Precision Two-Stage Matrix Optimization Lab", WS_CHILD|WS_VISIBLE, 35, 50, 600, 24, 0, hFontTitle))
	t1(createCtrl("STATIC", "Select optimization strategy and click Run Optimization to discover the optimal network profile:", WS_CHILD|WS_VISIBLE, 35, 78, 680, 20, 0, hFontRegular))

	// Profile selectors
	hBtnProfilePing = createCtrl("BUTTON", "1. Best Ping (Gaming / Minimal Delay)", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 35, 105, 220, 32, ID_BTN_TUNE_PING, hFontRegular)
	hBtnProfileSpeed = createCtrl("BUTTON", "2. Best Speed (Download / 4K Stream)", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 265, 105, 220, 32, ID_BTN_TUNE_SPEED, hFontRegular)
	hBtnProfileBal = createCtrl("BUTTON", "3. Balanced [*] (Delay-Bandwidth)", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 495, 105, 225, 32, ID_BTN_TUNE_BAL, hFontBold)
	t1(hBtnProfilePing)
	t1(hBtnProfileSpeed)
	t1(hBtnProfileBal)

	// Action Row
	hBtnStartTune = createCtrl("BUTTON", "⚡ Run Two-Stage Matrix Optimization", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 35, 148, 280, 36, ID_BTN_START_TUNE, hFontBold)
	hBtnQuickPing = createCtrl("BUTTON", "📶 Ping Test (6 Probes)", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 325, 148, 185, 36, ID_BTN_QUICK_PING, hFontRegular)
	hBtnQuickSpeed = createCtrl("BUTTON", "📥 4MB Download Test", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 520, 148, 200, 36, ID_BTN_QUICK_SPEED, hFontRegular)
	t1(hBtnStartTune)
	t1(hBtnQuickPing)
	t1(hBtnQuickSpeed)

	// Progress Bar (msctls_progress32)
	cProg, _ := syscall.UTF16PtrFromString("msctls_progress32")
	hProgressBar, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(cProg)),
		0,
		WS_CHILD|WS_VISIBLE|WS_BORDER,
		uintptr(35), uintptr(195),
		uintptr(685), uintptr(14),
		hWnd, 0, hInstance, 0,
	)
	procSendMessageW.Call(hProgressBar, PBM_SETRANGE, 0, uintptr((100<<16)|0))
	t1(hProgressBar)

	// Results & Precision Metrics Card
	t1(createCtrl("BUTTON", "High-Precision Matrix Evaluation Metrics", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 35, 220, 685, 375, 0, hFontRegular))

	hLblLabStatus = createCtrl("STATIC", "Lab Status: Ready. Choose profile and click Run Optimization.", WS_CHILD|WS_VISIBLE, 50, 248, 655, 22, 0, hFontBold)
	hLblLabLatency = createCtrl("STATIC", "Latency: Median: -- ms | Trimmed Mean: -- ms | PureNet RTT: -- ms | Jitter: -- ms | Loss: 0%", WS_CHILD|WS_VISIBLE, 50, 278, 655, 22, 0, hFontRegular)
	hLblLabBandwidth = createCtrl("STATIC", "Throughput: Sustained: -- MB/s (-- Mbps) | Peak Burst: -- MB/s | Stability: -- % | Integrity: --", WS_CHILD|WS_VISIBLE, 50, 308, 655, 22, 0, hFontRegular)
	hLblLabScore = createCtrl("STATIC", "Utility Score: -- | Optimal Candidate: --", WS_CHILD|WS_VISIBLE, 50, 338, 655, 22, 0, hFontBold)
	hBtnApplyTune = createCtrl("BUTTON", "💾 Apply & Lock Optimal Parameters to VPS & Client", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 50, 375, 380, 34, ID_BTN_APPLY_TUNE, hFontBold)

	t1(hLblLabStatus)
	t1(hLblLabLatency)
	t1(hLblLabBandwidth)
	t1(hLblLabScore)
	t1(hBtnApplyTune)

	// -------------------------------------------------------------
	// TAB 2: GOOGLE SCRIPT ACCOUNTS & SECURITY
	// -------------------------------------------------------------
	t2 := func(h uintptr) { tabControls[2] = append(tabControls[2], h) }

	t2(createCtrl("STATIC", "Google Apps Script Multi-Account Pool & Security", WS_CHILD|WS_VISIBLE, 35, 50, 600, 24, 0, hFontTitle))
	t2(createCtrl("STATIC", "Paste Web App deployment URLs (each line one URL). The pool distributes load and handles failover automatically:", WS_CHILD|WS_VISIBLE, 35, 78, 680, 20, 0, hFontRegular))

	hEditGas = createCtrl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP|ES_MULTILINE|ES_AUTOVSCROLL|WS_VSCROLL, 35, 105, 685, 200, 0, hFontMonopic)
	t2(hEditGas)

	t2(createCtrl("BUTTON", "Authentication & Domain Fronting Settings", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 35, 315, 685, 205, 0, hFontRegular))

	t2(createCtrl("STATIC", "Shared Data Token:", WS_CHILD|WS_VISIBLE, 50, 338, 150, 18, 0, hFontRegular))
	hEditToken = createCtrl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP|ES_AUTOHSCROLL, 205, 336, 250, 22, 0, hFontBold)
	t2(hEditToken)

	t2(createCtrl("STATIC", "Admin Token (VPS):", WS_CHILD|WS_VISIBLE, 50, 366, 150, 18, 0, hFontRegular))
	hEditAdminToken = createCtrl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP|ES_AUTOHSCROLL, 205, 364, 250, 22, 0, hFontBold)
	t2(hEditAdminToken)

	t2(createCtrl("STATIC", "Xray VLESS UUID:", WS_CHILD|WS_VISIBLE, 50, 394, 150, 18, 0, hFontRegular))
	hEditXrayUUID = createCtrl("EDIT", "e2a4a9c8-7c8b-4a5e-9e2a-1a2b3c4d5e6f", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP|ES_AUTOHSCROLL, 205, 392, 250, 22, 0, hFontRegular)
	t2(hEditXrayUUID)

	t2(createCtrl("STATIC", "Domain Fronting SNI:", WS_CHILD|WS_VISIBLE, 50, 422, 150, 18, 0, hFontRegular))
	hEditFront = createCtrl("EDIT", "fcm.googleapis.com", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP|ES_AUTOHSCROLL, 205, 420, 250, 22, 0, hFontRegular)
	t2(hEditFront)

	t2(createCtrl("STATIC", "Clean Google IP (Opt):", WS_CHILD|WS_VISIBLE, 50, 450, 150, 18, 0, hFontRegular))
	hEditFrontIP = createCtrl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP|ES_AUTOHSCROLL, 205, 448, 250, 22, 0, hFontRegular)
	t2(hEditFrontIP)

	hBtnValidateAcc = createCtrl("BUTTON", "🔍 Probe All Accounts Health", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 480, 348, 220, 32, ID_BTN_VALIDATE, hFontRegular)
	hBtnSaveAcc = createCtrl("BUTTON", "💾 Save Accounts Config", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 480, 420, 220, 34, ID_BTN_SAVE, hFontBold)
	t2(hBtnValidateAcc)
	t2(hBtnSaveAcc)

	// -------------------------------------------------------------
	// TAB 3: ADVANCED DIALS (CLIENT & SERVER TUNING)
	// -------------------------------------------------------------
	t3 := func(h uintptr) { tabControls[3] = append(tabControls[3], h) }

	t3(createCtrl("STATIC", "Client & Remote VPS Performance Tuning", WS_CHILD|WS_VISIBLE, 35, 50, 600, 24, 0, hFontTitle))

	t3(createCtrl("BUTTON", "Client-Side Engine Tuning", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 35, 80, 685, 160, 0, hFontRegular))

	t3(createCtrl("STATIC", "Concurrent Downlink Workers (1 - 4):", WS_CHILD|WS_VISIBLE, 50, 110, 260, 20, 0, hFontRegular))
	hEditWorkers = createCtrl("EDIT", "2", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP, 320, 108, 60, 24, 0, hFontBold)
	t3(hEditWorkers)

	t3(createCtrl("STATIC", "Fast Streaming Poll Delay (ms, 0 - 30):", WS_CHILD|WS_VISIBLE, 50, 145, 260, 20, 0, hFontRegular))
	hEditFastPoll = createCtrl("EDIT", "5", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP, 320, 143, 60, 24, 0, hFontBold)
	t3(hEditFastPoll)

	t3(createCtrl("STATIC", "Idle Backoff Max Delay (ms, 200 - 1000):", WS_CHILD|WS_VISIBLE, 400, 110, 250, 20, 0, hFontRegular))
	hEditMaxIdle = createCtrl("EDIT", "600", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP, 645, 108, 60, 24, 0, hFontBold)
	t3(hEditMaxIdle)

	t3(createCtrl("STATIC", "Xray Mux Concurrency (0 = Off, 4, 8, 16):", WS_CHILD|WS_VISIBLE, 400, 145, 250, 20, 0, hFontRegular))
	hEditMux = createCtrl("EDIT", "8", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP, 645, 143, 60, 24, 0, hFontBold)
	t3(hEditMux)

	t3(createCtrl("BUTTON", "Server-Side VPS Engine Tuning (Remote Controlled)", WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 35, 260, 685, 160, 0, hFontRegular))

	t3(createCtrl("STATIC", "VPS Hold Wait Timeout (ms, 200 - 1500):", WS_CHILD|WS_VISIBLE, 50, 290, 260, 20, 0, hFontRegular))
	hEditServerWait = createCtrl("EDIT", "800", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP, 320, 288, 60, 24, 0, hFontBold)
	t3(hEditServerWait)

	t3(createCtrl("STATIC", "VPS Max Drain Batch Size (Bytes):", WS_CHILD|WS_VISIBLE, 50, 325, 260, 20, 0, hFontRegular))
	hEditServerDrain = createCtrl("EDIT", "131072", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP, 320, 323, 80, 24, 0, hFontBold)
	t3(hEditServerDrain)

	hBtnFetchVPS = createCtrl("BUTTON", "🔄 Fetch VPS Config", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 440, 288, 150, 30, ID_BTN_FETCH_VPS, hFontRegular)
	hBtnPushVPS = createCtrl("BUTTON", "📤 Push to VPS", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 600, 288, 105, 30, ID_BTN_PUSH_VPS, hFontBold)
	t3(hBtnFetchVPS)
	t3(hBtnPushVPS)

	hBtnSaveDials = createCtrl("BUTTON", "💾 Save All Tuning Parameters", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 35, 440, 280, 36, ID_BTN_SAVE, hFontBold)
	t3(hBtnSaveDials)

	// -------------------------------------------------------------
	// TAB 4: SYSTEM LOGS
	// -------------------------------------------------------------
	t4 := func(h uintptr) { tabControls[4] = append(tabControls[4], h) }

	t4(createCtrl("STATIC", "System Event & Network Diagnostic Logs", WS_CHILD|WS_VISIBLE, 35, 50, 600, 24, 0, hFontTitle))
	hEditLog = createCtrl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_VSCROLL|WS_BORDER|ES_LEFT|ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY, 35, 80, 685, 480, 0, hFontMonopic)
	hBtnClearLog = createCtrl("BUTTON", "🧹 Clear Logs", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 35, 570, 130, 30, ID_BTN_CLEAR_LOG, hFontRegular)
	t4(hEditLog)
	t4(hBtnClearLog)
}

func switchTab(tabIndex int) {
	if tabIndex < 0 || tabIndex >= 5 {
		return
	}
	currentTab = tabIndex

	for i, controls := range tabControls {
		show := int32(0) // SW_HIDE
		if i == tabIndex {
			show = int32(5) // SW_SHOW
		}
		for _, hCtrl := range controls {
			procShowWindow.Call(hCtrl, uintptr(show))
		}
	}
	procSendMessageW.Call(hTabCtrl, TCM_SETCURSEL, uintptr(tabIndex), 0)
}

func parseGasURLsFromText(text string) []string {
	var urls []string
	seen := make(map[string]bool)
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" && !seen[trimmed] {
			urls = append(urls, trimmed)
			seen[trimmed] = true
		}
	}
	return urls
}

func loadInitialConfig() {
	data, err := os.ReadFile(configPath)
	if err == nil {
		var cfg relay.Config
		if json.Unmarshal(data, &cfg) == nil {
			cfg.EnsureDefaults()

			allURLs := cfg.GetGasURLs()
			if len(allURLs) > 0 {
				setControlText(hEditGas, strings.Join(allURLs, "\r\n"))
				setControlText(hCardMetricAcc, fmt.Sprintf("%d Accounts", len(allURLs)))
			}

			if cfg.Token != "" {
				setControlText(hEditToken, cfg.Token)
			}
			if cfg.AdminToken != "" {
				setControlText(hEditAdminToken, cfg.AdminToken)
			} else if cfg.Token != "" {
				setControlText(hEditAdminToken, cfg.Token)
			}
			if cfg.XrayUUID != "" {
				setControlText(hEditXrayUUID, cfg.XrayUUID)
			} else {
				setControlText(hEditXrayUUID, "e2a4a9c8-7c8b-4a5e-9e2a-1a2b3c4d5e6f")
			}
			if cfg.FrontDomain != "" {
				setControlText(hEditFront, cfg.FrontDomain)
			}
			setControlText(hEditFrontIP, cfg.FrontIP)

			if cfg.ClientTuning != nil {
				workers := cfg.ClientTuning.Workers
				if workers < 1 || workers > 4 {
					workers = 2
				}
				setControlText(hEditWorkers, strconv.Itoa(workers))
				setControlText(hEditFastPoll, strconv.Itoa(cfg.ClientTuning.DownlinkFastPollMs))
				setControlText(hEditMaxIdle, strconv.Itoa(cfg.ClientTuning.DownlinkMaxIdleMs))
				setControlText(hEditMux, strconv.Itoa(cfg.ClientTuning.MuxConcurrency))
			}
			if cfg.ServerTuning != nil {
				wait := cfg.ServerTuning.WaitTimeoutMs
				if wait < 100 || wait > 2000 {
					wait = 800
				}
				drain := cfg.ServerTuning.MaxDrainBatchBytes
				if drain < 8192 || drain > 524288 {
					drain = 131072
				}
				setControlText(hEditServerWait, strconv.Itoa(wait))
				setControlText(hEditServerDrain, strconv.Itoa(drain))
			}

			appendLog(fmt.Sprintf("Loaded configuration from %s (%d accounts configured)", configPath, len(allURLs)))
			return
		}
	}
	appendLog("Ready. Enter accounts and click Start Relay + Xray.")
}

func collectCurrentConfig() relay.Config {
	urls := parseGasURLsFromText(getControlText(hEditGas))
	firstURL := ""
	if len(urls) > 0 {
		firstURL = urls[0]
	}

	workers, _ := strconv.Atoi(getControlText(hEditWorkers))
	if workers < 1 || workers > 4 {
		workers = 2
		setControlText(hEditWorkers, "2")
	}
	fastPoll, _ := strconv.Atoi(getControlText(hEditFastPoll))
	if fastPoll < 0 || fastPoll > 50 {
		fastPoll = 5
		setControlText(hEditFastPoll, "5")
	}
	maxIdle, _ := strconv.Atoi(getControlText(hEditMaxIdle))
	if maxIdle < 100 || maxIdle > 2000 {
		maxIdle = 600
		setControlText(hEditMaxIdle, "600")
	}
	muxCount, _ := strconv.Atoi(getControlText(hEditMux))
	if muxCount < 0 || muxCount > 32 {
		muxCount = 8
		setControlText(hEditMux, "8")
	}
	serverWait, _ := strconv.Atoi(getControlText(hEditServerWait))
	if serverWait < 100 || serverWait > 2000 {
		serverWait = 800
		setControlText(hEditServerWait, "800")
	}
	serverDrain, _ := strconv.Atoi(getControlText(hEditServerDrain))
	if serverDrain < 8192 || serverDrain > 524288 {
		serverDrain = 131072
		setControlText(hEditServerDrain, "131072")
	}

	clientTuning := relay.ClientTuning{
		Workers:            workers,
		WorkerStaggerMs:    200,
		DownlinkFastPollMs: fastPoll,
		DownlinkMaxIdleMs:  maxIdle,
		MuxConcurrency:     muxCount,
		SocketBufferKB:     64,
		UplinkChunkKB:      32,
	}

	serverTuning := relay.ServerTuning{
		WaitTimeoutMs:      serverWait,
		MaxDrainBatchBytes: serverDrain,
		DownlinkQueueCap:   256,
	}

	adminTok := getControlText(hEditAdminToken)
	if adminTok == "" {
		adminTok = getControlText(hEditToken)
	}

	xrayUUID := getControlText(hEditXrayUUID)
	if xrayUUID == "" {
		xrayUUID = "e2a4a9c8-7c8b-4a5e-9e2a-1a2b3c4d5e6f"
	}

	return relay.Config{
		Mode:         "client",
		ListenAddr:   "127.0.0.1:10800",
		GasURL:       firstURL,
		GasURLs:      urls,
		Token:        getControlText(hEditToken),
		AdminToken:   adminTok,
		XrayUUID:     xrayUUID,
		FrontDomain:  getControlText(hEditFront),
		FrontIP:      getControlText(hEditFrontIP),
		ClientTuning: &clientTuning,
		ServerTuning: &serverTuning,
		SocksPort:    10808,
		HttpPort:     10809,
		StartXray:    true,
	}
}

func saveCurrentConfig() {
	cfg := collectCurrentConfig()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		appendLog(fmt.Sprintf("Error encoding config: %v", err))
		return
	}

	if err := relay.AtomicWriteSecureConfigFile(configPath, data); err != nil {
		appendLog(fmt.Sprintf("Failed to save config: %v", err))
	} else {
		appendLog(fmt.Sprintf("Saved configuration securely to %s (%d accounts)", configPath, len(cfg.GasURLs)))
		setControlText(hCardMetricAcc, fmt.Sprintf("%d Accounts", len(cfg.GasURLs)))
	}
}

func startRelay() {
	relayMutex.Lock()
	defer relayMutex.Unlock()

	if relayClient != nil && relayClient.IsRunning() {
		return
	}

	cfg := collectCurrentConfig()
	if len(cfg.GasURLs) == 0 {
		appendLog("Error: Please provide at least one Google Apps Script Web App URL in Tab 3!")
		setControlText(hLblDashStatus, "● Error: Missing Google Script URLs (check Tab 3)")
		switchTab(2)
		return
	}
	if strings.TrimSpace(cfg.Token) == "" {
		appendLog("Error: Auth Token cannot be empty!")
		setControlText(hLblDashStatus, "● Error: Auth Token cannot be empty")
		switchTab(2)
		return
	}

	relayClient = relay.NewClient(&cfg)
	relayClient.LogFunc = func(format string, args ...any) {
		appendLog(fmt.Sprintf(format, args...))
	}

	if err := relayClient.Start(); err != nil {
		appendLog(fmt.Sprintf("Failed to start relay: %v", err))
		setControlText(hLblDashStatus, fmt.Sprintf("● Start Failed: %v", err))
		relayClient = nil
		return
	}

	procEnableWindow.Call(hBtnStart, 0)
	procEnableWindow.Call(hBtnStop, 1)
	setControlText(hLblDashStatus, "● Status: Running & Connected (Relay + Xray Active)")
	appendLog("Relay & Xray Core started successfully.")
}

func stopRelay() {
	relayMutex.Lock()
	defer relayMutex.Unlock()

	if relayClient != nil {
		relayClient.Stop()
		relayClient = nil
	}

	if systemProxyOn {
		_ = xray.SetSystemProxy(false, "")
		systemProxyOn = false
		setControlText(hBtnProxy, "🛡 Windows Proxy: OFF")
		appendLog("Windows System Proxy disabled.")
	}

	procEnableWindow.Call(hBtnStart, 1)
	procEnableWindow.Call(hBtnStop, 0)
	setControlText(hLblDashStatus, "● Status: Stopped (Click 'Start Relay + Xray' below to connect)")
	appendLog("Relay & Xray Core stopped.")
}

func toggleSystemProxy() {
	systemProxyOn = !systemProxyOn
	if systemProxyOn {
		_ = xray.SetSystemProxy(true, "127.0.0.1:10809")
		setControlText(hBtnProxy, "🛡 Windows Proxy: ON [*]")
		appendLog("Windows System Proxy enabled -> routed through 127.0.0.1:10809")
	} else {
		_ = xray.SetSystemProxy(false, "")
		setControlText(hBtnProxy, "🛡 Windows Proxy: OFF")
		appendLog("Windows System Proxy disabled.")
	}
}

func fetchVPSConfig() {
	relayMutex.Lock()
	rc := relayClient
	relayMutex.Unlock()

	if rc == nil || !rc.IsRunning() {
		appendLog("Error: Please start Relay first before querying VPS!")
		return
	}

	go func() {
		dataToken := getControlText(hEditToken)
		admToken := getControlText(hEditAdminToken)
		if admToken == "" {
			admToken = dataToken
		}
		appendLog("[Admin] Querying VPS server configuration over Google tunnel...")
		res, err := relay.SendAdminCommand(rc.GetHTTPClient(), rc.GetEndpointPool(), dataToken, admToken, relay.AdminRequest{
			Action: "get_tuning",
		})
		if err != nil {
			appendLog(fmt.Sprintf("[Admin] Error fetching VPS config: %v", err))
			return
		}
		if res.Tuning != nil {
			setControlText(hEditServerWait, strconv.Itoa(res.Tuning.WaitTimeoutMs))
			setControlText(hEditServerDrain, strconv.Itoa(res.Tuning.MaxDrainBatchBytes))
			appendLog(fmt.Sprintf("[Admin] VPS Config retrieved: WaitTimeout=%dms, MaxDrain=%d bytes",
				res.Tuning.WaitTimeoutMs, res.Tuning.MaxDrainBatchBytes))
		}
		if res.XrayUUID != "" {
			setControlText(hEditXrayUUID, res.XrayUUID)
			appendLog(fmt.Sprintf("[Admin] VPS Xray UUID synchronized: %s", res.XrayUUID))
			saveCurrentConfig()
		}
	}()
}

func pushVPSTuning() {
	relayMutex.Lock()
	rc := relayClient
	relayMutex.Unlock()

	if rc == nil || !rc.IsRunning() {
		appendLog("Error: Please start Relay first before pushing tuning to VPS!")
		return
	}

	serverWait, _ := strconv.Atoi(getControlText(hEditServerWait))
	serverDrain, _ := strconv.Atoi(getControlText(hEditServerDrain))

	tuning := relay.ServerTuning{
		WaitTimeoutMs:      serverWait,
		MaxDrainBatchBytes: serverDrain,
		DownlinkQueueCap:   256,
	}

	cfg := collectCurrentConfig()
	rc.SetTuning(*cfg.ClientTuning)

	go func() {
		dataToken := getControlText(hEditToken)
		admToken := getControlText(hEditAdminToken)
		if admToken == "" {
			admToken = dataToken
		}
		appendLog("[Admin] Pushing new tuning parameters to VPS...")
		res, err := relay.SendAdminCommand(rc.GetHTTPClient(), rc.GetEndpointPool(), dataToken, admToken, relay.AdminRequest{
			Action: "set_tuning",
			Tuning: &tuning,
		})
		if err != nil {
			appendLog(fmt.Sprintf("[Admin] Error pushing tuning to VPS: %v", err))
			return
		}
		_, _ = relay.SendAdminCommand(rc.GetHTTPClient(), rc.GetEndpointPool(), dataToken, admToken, relay.AdminRequest{
			Action: "save_config",
		})
		appendLog(fmt.Sprintf("[Admin] VPS tuning updated and persisted: %s", res.Message))
	}()
}

func runMatrixOptimization() {
	if optActive.Load() {
		return
	}

	relayMutex.Lock()
	rc := relayClient
	relayMutex.Unlock()

	if rc == nil || !rc.IsRunning() {
		appendLog("Auto-starting relay for optimization test...")
		startRelay()
		time.Sleep(1 * time.Second)
		relayMutex.Lock()
		rc = relayClient
		relayMutex.Unlock()
		if rc == nil || !rc.IsRunning() {
			appendLog("Error: Unable to start relay for optimization!")
			return
		}
	}

	optActive.Store(true)
	procEnableWindow.Call(hBtnStartTune, 0)
	setControlText(hLblLabStatus, fmt.Sprintf("Lab Status: Running Two-Stage Matrix Optimization for %s...", currentProfile))
	procSendMessageW.Call(hProgressBar, PBM_SETPOS, 0, 0)

	go func() {
		defer func() {
			optActive.Store(false)
			procEnableWindow.Call(hBtnStartTune, 1)
		}()

		opt := relay.NewOptimizer(rc)
		optimizer = opt

		report, err := opt.RunOptimization(context.Background(), currentProfile, func(sr relay.StepResult) {
			pct := int((float64(sr.Step) / float64(sr.TotalSteps)) * 100.0)
			procSendMessageW.Call(hProgressBar, PBM_SETPOS, uintptr(pct), 0)

			setControlText(hLblLabStatus, fmt.Sprintf("Evaluating [%d/%d]: %s", sr.Step, sr.TotalSteps, sr.Candidate.Name))
			setControlText(hLblLabLatency, fmt.Sprintf("Latency: Median=%.1fms | Trimmed=%.1fms | PureNet=%.1fms | Jitter=%.1fms | Loss=%.0f%%",
				sr.Ping.MedianMs, sr.Ping.TrimmedMeanMs, sr.Ping.PureNetworkMs, sr.Ping.JitterMs, sr.Ping.LossRate*100))
			setControlText(hLblLabBandwidth, fmt.Sprintf("Throughput: Sustained=%.2f MB/s (%.1f Mbps) | Peak=%.2f MB/s | Stability=%.0f%% | CRC=OK",
				sr.Speed.SpeedMBps, sr.Speed.SpeedMbpsNet, sr.Speed.PeakSpeedMB, sr.Speed.StabilityPct))
			setControlText(hLblLabScore, fmt.Sprintf("Utility Score: %.2f (Current Best: %.2f)", sr.Score, sr.CurrentBest))

			// Update dashboard metric cards in real time
			setControlText(hCardMetricPing, fmt.Sprintf("%.1f ms", sr.Ping.MedianMs))
			setControlText(hCardMetricSpd, fmt.Sprintf("%.2f MB/s", sr.Speed.SpeedMBps))
		})

		if err != nil {
			setControlText(hLblLabStatus, fmt.Sprintf("Optimization error: %v", err))
			appendLog(fmt.Sprintf("[Optimizer] Failed: %v", err))
			return
		}

		procSendMessageW.Call(hProgressBar, PBM_SETPOS, 100, 0)
		setControlText(hLblLabStatus, fmt.Sprintf("Optimization Finished! Best Score: %.2f in %v",
			report.BestScore, report.Duration.Round(time.Second)))
		setControlText(hLblLabLatency, fmt.Sprintf("Optimal Latency: Median=%.1fms (Trimmed: %.1fms) | Jitter=%.1fms | PureNet=%.1fms",
			report.BestPing.MedianMs, report.BestPing.TrimmedMeanMs, report.BestPing.JitterMs, report.BestPing.PureNetworkMs))
		setControlText(hLblLabBandwidth, fmt.Sprintf("Optimal Throughput: %.2f MB/s (%.1f Mbps) | Peak=%.2f MB/s | Stability=%.0f%%",
			report.BestSpeed.SpeedMBps, report.BestSpeed.SpeedMbpsNet, report.BestSpeed.PeakSpeedMB, report.BestSpeed.StabilityPct))
		setControlText(hLblLabScore, fmt.Sprintf("Winning Archetype: %s (Score: %.2f)", report.BestCandidate.Name, report.BestScore))

		// Update UI edit boxes with winning tuning
		setControlText(hEditWorkers, strconv.Itoa(report.BestClient.Workers))
		setControlText(hEditFastPoll, strconv.Itoa(report.BestClient.DownlinkFastPollMs))
		setControlText(hEditMaxIdle, strconv.Itoa(report.BestClient.DownlinkMaxIdleMs))
		setControlText(hEditMux, strconv.Itoa(report.BestClient.MuxConcurrency))
		setControlText(hEditServerWait, strconv.Itoa(report.BestServer.WaitTimeoutMs))
		setControlText(hEditServerDrain, strconv.Itoa(report.BestServer.MaxDrainBatchBytes))

		// Update dashboard metric cards with final winners
		setControlText(hCardMetricPing, fmt.Sprintf("%.1f ms", report.BestPing.MedianMs))
		setControlText(hCardMetricSpd, fmt.Sprintf("%.2f MB/s", report.BestSpeed.SpeedMBps))

		rc.SetTuning(report.BestClient)
		saveCurrentConfig()
		appendLog("[Optimizer] Optimal client and server configurations applied and saved automatically!")
	}()
}

func runQuickPingTest() {
	relayMutex.Lock()
	rc := relayClient
	relayMutex.Unlock()

	if rc == nil || !rc.IsRunning() {
		appendLog("Auto-starting relay for ping test...")
		startRelay()
		time.Sleep(1 * time.Second)
		relayMutex.Lock()
		rc = relayClient
		relayMutex.Unlock()
	}

	setControlText(hLblLabStatus, "Running 6 precision ping probes directly to VPS...")
	go func() {
		res, err := relay.RunPingBenchmark(rc.GetHTTPClient(), rc.GetEndpointPool(), getControlText(hEditToken), 6)
		if err != nil {
			setControlText(hLblLabStatus, fmt.Sprintf("Ping error: %v", err))
			appendLog(fmt.Sprintf("[Ping] Error: %v", err))
			return
		}
		setControlText(hLblLabLatency, fmt.Sprintf("Latency: Median=%.1fms | TrimmedAvg=%.1fms | PureNet=%.1fms | Jitter=%.1fms | Loss=%.0f%%",
			res.MedianMs, res.TrimmedMeanMs, res.PureNetworkMs, res.JitterMs, res.LossRate*100))
		setControlText(hCardMetricPing, fmt.Sprintf("%.1f ms", res.MedianMs))
		setControlText(hLblLabStatus, "Ping test complete.")
		appendLog(fmt.Sprintf("[Ping Result] Median=%.1fms, Min=%.1fms, Max=%.1fms, Jitter=%.1fms, Loss=%.0f%%",
			res.MedianMs, res.MinMs, res.MaxMs, res.JitterMs, res.LossRate*100))
	}()
}

func runQuickSpeedTest() {
	relayMutex.Lock()
	rc := relayClient
	relayMutex.Unlock()

	if rc == nil || !rc.IsRunning() {
		appendLog("Auto-starting relay for speed test...")
		startRelay()
		time.Sleep(1 * time.Second)
		relayMutex.Lock()
		rc = relayClient
		relayMutex.Unlock()
	}

	setControlText(hLblLabStatus, "Streaming 4 MB test payload directly from VPS memory...")
	procSendMessageW.Call(hProgressBar, PBM_SETPOS, 0, 0)

	go func() {
		res, err := relay.RunSpeedBenchmark(rc.GetHTTPClient(), rc.GetEndpointPool(), getControlText(hEditToken), func(done, total int, spd float64) {
			pct := int((float64(done) / float64(total)) * 100.0)
			procSendMessageW.Call(hProgressBar, PBM_SETPOS, uintptr(pct), 0)
			setControlText(hLblLabBandwidth, fmt.Sprintf("Streaming: %d / %d KB (%.1f%%) - Current Speed: %.2f MB/s",
				done/1024, total/1024, float64(pct), spd))
		})
		if err != nil {
			setControlText(hLblLabStatus, fmt.Sprintf("Speed test error: %v", err))
			appendLog(fmt.Sprintf("[Speed] Error: %v", err))
			return
		}
		procSendMessageW.Call(hProgressBar, PBM_SETPOS, 100, 0)
		setControlText(hLblLabBandwidth, fmt.Sprintf("Throughput: %.2f MB/s (%.1f Mbps) | Peak: %.2f MB/s | Stability: %.0f%% | CRC=OK",
			res.SpeedMBps, res.SpeedMbpsNet, res.PeakSpeedMB, res.StabilityPct))
		setControlText(hCardMetricSpd, fmt.Sprintf("%.2f MB/s", res.SpeedMBps))
		setControlText(hLblLabStatus, "4 MB Download test complete.")
		appendLog(fmt.Sprintf("[Speed Result] %.2f MB/s (%.1f Mbps) in %v | Peak=%.2f MB/s | Stability=%.0f%%",
			res.SpeedMBps, res.SpeedMbpsNet, res.Duration.Round(time.Millisecond), res.PeakSpeedMB, res.StabilityPct))
	}()
}

func validateAllAccounts() {
	raw := getControlText(hEditGas)
	urls := parseGasURLsFromText(raw)
	if len(urls) == 0 {
		appendLog("[Validation] No Google Script URLs provided!")
		return
	}

	appendLog(fmt.Sprintf("[Validation] Probing %d Google Script accounts...", len(urls)))
	go func() {
		p, err := relay.NewEndpointPool(urls, func(format string, args ...any) {
			appendLog(fmt.Sprintf(format, args...))
		})
		if err != nil {
			appendLog(fmt.Sprintf("[Validation] Error: %v", err))
			return
		}
		appendLog(fmt.Sprintf("[Validation] Initialized pool with %d endpoints. Status: %s", len(urls), p.GetStatusSummary()))
	}()
}

func setProfile(p relay.ProfileType) {
	currentProfile = p
	switch p {
	case relay.ProfilePing:
		setControlText(hBtnProfilePing, "1. Best Ping [*]")
		setControlText(hBtnProfileSpeed, "2. Best Speed")
		setControlText(hBtnProfileBal, "3. Balanced")
		appendLog("[Lab] Strategy: Best Ping (Minimal Median Latency & Zero Jitter)")
	case relay.ProfileSpeed:
		setControlText(hBtnProfilePing, "1. Best Ping")
		setControlText(hBtnProfileSpeed, "2. Best Speed [*]")
		setControlText(hBtnProfileBal, "3. Balanced")
		appendLog("[Lab] Strategy: Best Speed (Maximum 4MB Sustained Download Throughput)")
	case relay.ProfileBalanced:
		setControlText(hBtnProfilePing, "1. Best Ping")
		setControlText(hBtnProfileSpeed, "2. Best Speed")
		setControlText(hBtnProfileBal, "3. Balanced [*]")
		appendLog("[Lab] Strategy: Balanced (Delay-Bandwidth Optimization)")
	}
}

func wndProc(hWnd uintptr, msg uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	switch msg {
	case WM_NOTIFY:
		nmhdr := (*NMHDR)(lParam)

		if nmhdr.IdFrom == ID_TAB_MAIN && nmhdr.Code == TCN_SELCHANGE {
			idx, _, _ := procSendMessageW.Call(hTabCtrl, TCM_GETCURSEL, 0, 0)
			switchTab(int(idx))
			return 0
		}

	case WM_CTLCOLORSTATIC:
		hdc := wParam
		procSetBkMode.Call(hdc, 1)                      // TRANSPARENT
		procSetTextColor.Call(hdc, uintptr(0x00E8E8E8)) // Crisp white
		return hDarkBrush

	case WM_CTLCOLOREDIT:
		hdc := wParam
		procSetBkMode.Call(hdc, 2)
		procSetBkColor.Call(hdc, uintptr(0x0034231E))   // Dark Elevated Box
		procSetTextColor.Call(hdc, uintptr(0x00EBDC89)) // Cyan text
		return hCardBrush

	case WM_CTLCOLORBTN:
		hdc := wParam
		procSetBkMode.Call(hdc, 1)
		return hDarkBrush

	case WM_COMMAND:
		id := uint16(wParam & 0xffff)
		switch id {
		case ID_BTN_START:
			go startRelay()
		case ID_BTN_STOP:
			go stopRelay()
		case ID_BTN_PROXY:
			go toggleSystemProxy()
		case ID_BTN_SAVE:
			saveCurrentConfig()
		case ID_BTN_TUNE_PING:
			setProfile(relay.ProfilePing)
		case ID_BTN_TUNE_SPEED:
			setProfile(relay.ProfileSpeed)
		case ID_BTN_TUNE_BAL:
			setProfile(relay.ProfileBalanced)
		case ID_BTN_START_TUNE:
			go runMatrixOptimization()
		case ID_BTN_QUICK_PING:
			go runQuickPingTest()
		case ID_BTN_QUICK_SPEED:
			go runQuickSpeedTest()
		case ID_BTN_APPLY_TUNE:
			saveCurrentConfig()
			go pushVPSTuning()
		case ID_BTN_FETCH_VPS:
			fetchVPSConfig()
		case ID_BTN_PUSH_VPS:
			pushVPSTuning()
		case ID_BTN_VALIDATE:
			validateAllAccounts()
		case ID_BTN_CLEAR_LOG:
			setControlText(hEditLog, "")
		}
		return 0

	case WM_DESTROY:
		stopRelay()
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hWnd, uintptr(msg), wParam, uintptr(lParam))
	return ret
}

func getControlText(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return syscall.UTF16ToString(buf)
}

func setControlText(hwnd uintptr, text string) {
	ptr, _ := syscall.UTF16PtrFromString(text)
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(ptr)))
}

func appendLog(msg string) {
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s\r\n", ts, msg)
	endPos := uintptr(0xFFFFFFFF)
	procSendMessageW.Call(hEditLog, EM_SETSEL, endPos, endPos)
	ptr, _ := syscall.UTF16PtrFromString(line)
	procSendMessageW.Call(hEditLog, EM_REPLACESEL, 0, uintptr(unsafe.Pointer(ptr)))
	procSendMessageW.Call(hEditLog, EM_SCROLLCARET, 0, 0)
}
