//go:build windows

package main

import (
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Native Win32 progress window (status text + progress bar + cancel
// button) without external dependencies. The window class and the message
// loop run on their own, locked OS thread; SetStatus/SetProgress may be
// called from any goroutine (SendMessage is thread-safe).

const (
	wsCaption = 0x00C00000
	wsSysMenu = 0x00080000
	wsVisible = 0x10000000
	wsChild   = 0x40000000

	ssNoPrefix = 0x00000080

	pbsMarquee    = 0x00000008
	pbmSetPos     = 0x0402
	pbmSetRange32 = 0x0406
	pbmSetMarquee = 0x040A

	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmSetFont        = 0x0030
	wmSetIcon        = 0x0080
	wmCommand        = 0x0111
	wmCtlColorStatic = 0x0138
	wmAppClose       = 0x8000 + 1 // WM_APP + 1: close window from its own thread

	colorWindow    = 5
	transparentBk  = 1
	swShow         = 5
	smCxScreen     = 0
	smCyScreen     = 1
	fwNormal       = 400
	defaultCharset = 1
	cleartype      = 5

	cancelBtnID = 1
)

// GWL_STYLE is -16; expressed as a uintptr in two's complement.
const gwlStyle = ^uintptr(15)

var (
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")

	procRegisterClassEx    = user32.NewProc("RegisterClassExW")
	procCreateWindowEx     = user32.NewProc("CreateWindowExW")
	procDefWindowProc      = user32.NewProc("DefWindowProcW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procPostQuitMessage    = user32.NewProc("PostQuitMessage")
	procGetMessage         = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessage    = user32.NewProc("DispatchMessageW")
	procShowWindow         = user32.NewProc("ShowWindow")
	procUpdateWindow       = user32.NewProc("UpdateWindow")
	procSetForegroundWin   = user32.NewProc("SetForegroundWindow")
	procSendMessage        = user32.NewProc("SendMessageW")
	procPostMessage        = user32.NewProc("PostMessageW")
	procSetWindowText      = user32.NewProc("SetWindowTextW")
	procSetWindowLong      = user32.NewProc("SetWindowLongW")
	procEnableWindow       = user32.NewProc("EnableWindow")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procAdjustWindowRect   = user32.NewProc("AdjustWindowRect")
	procGetSysColorBrush   = user32.NewProc("GetSysColorBrush")
	procLoadCursor         = user32.NewProc("LoadCursorW")
	procLoadIcon           = user32.NewProc("LoadIconW")
	procGetDpiForSystem    = user32.NewProc("GetDpiForSystem")
	procGetModuleHandle    = kernel32.NewProc("GetModuleHandleW")
	procSetBkMode          = gdi32.NewProc("SetBkMode")
	procCreateFont         = gdi32.NewProc("CreateFontW")
	procInitCommonControls = comctl32.NewProc("InitCommonControlsEx")

	classOnce    sync.Once
	classNamePtr *uint16

	appIconOnce   sync.Once
	appIconHandle uintptr
	appIconResID  uintptr

	// At most one progress window exists at a time; wndProc needs access
	// to it in order to attribute cancel clicks.
	activeProgress atomic.Pointer[progressWin]
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	_       uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
	_       uint32
}

type rect struct {
	left, top, right, bottom int32
}

func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmClose:
		// X button = cancel; the window is closed by the install flow.
		if p := activeProgress.Load(); p != nil {
			p.requestCancel()
		}
		return 0
	case wmCommand:
		if wParam&0xffff == cancelBtnID && wParam>>16 == 0 { // BN_CLICKED
			if p := activeProgress.Load(); p != nil {
				p.requestCancel()
			}
		}
		return 0
	case wmAppClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	case wmCtlColorStatic:
		procSetBkMode.Call(wParam, transparentBk)
		brush, _, _ := procGetSysColorBrush.Call(colorWindow)
		return brush
	}
	ret, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return ret
}

// loadAppIcon looks for the application icon embedded in the EXE. The
// resource ID assigned by rsrc depends on which resources were embedded
// (the manifest occupies ID 1, for example), so the first IDs are tried
// instead of assuming a fixed one.
func loadAppIcon() {
	appIconOnce.Do(func() {
		hInst, _, _ := procGetModuleHandle.Call(0)
		for id := uintptr(1); id <= 32; id++ {
			if h, _, _ := procLoadIcon.Call(hInst, id); h != 0 {
				appIconHandle = h
				appIconResID = id
				return
			}
		}
	})
}

// appIcon returns the icon handle of the application icon (0 if none).
func appIcon() uintptr {
	loadAppIcon()
	return appIconHandle
}

// appIconID returns the resource ID of the application icon (0 if none).
func appIconID() uintptr {
	loadAppIcon()
	return appIconResID
}

func registerClass() {
	classOnce.Do(func() {
		type initCommonControlsEx struct {
			size uint32
			icc  uint32
		}
		icc := initCommonControlsEx{size: 8, icc: 0x00000020} // ICC_PROGRESS_CLASS
		procInitCommonControls.Call(uintptr(unsafe.Pointer(&icc)))

		classNamePtr, _ = syscall.UTF16PtrFromString("CodePortableProgress")
		hInst, _, _ := procGetModuleHandle.Call(0)
		cursor, _, _ := procLoadCursor.Call(0, 32512) // IDC_ARROW
		icon := appIcon()

		wc := wndClassEx{
			cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
			lpfnWndProc:   syscall.NewCallback(wndProc),
			hInstance:     hInst,
			hIcon:         icon,
			hIconSm:       icon,
			hCursor:       cursor,
			hbrBackground: colorWindow + 1,
			lpszClassName: classNamePtr,
		}
		procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	})
}

type progressWin struct {
	hwnd     uintptr
	label    uintptr
	bar      uintptr
	btn      uintptr
	marquee  bool
	canceled atomic.Bool
	wg       sync.WaitGroup
}

// newProgressWin creates the progress window and starts its message loop
// on its own thread.
func newProgressWin(title string) *progressWin {
	p := &progressWin{}
	created := make(chan struct{})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		runtime.LockOSThread()
		p.create(title)
		activeProgress.Store(p)
		close(created)
		var m winMsg
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	<-created
	return p
}

func (p *progressWin) create(title string) {
	registerClass()
	hInst, _, _ := procGetModuleHandle.Call(0)

	dpi := uintptr(96)
	if procGetDpiForSystem.Find() == nil {
		if d, _, _ := procGetDpiForSystem.Call(); d != 0 {
			dpi = d
		}
	}
	scale := func(v int) int32 { return int32(v * int(dpi) / 96) }

	const pad, labelH, barH, btnW, btnH, clientW = 14, 20, 18, 90, 26, 440
	clientH := pad + labelH + 10 + barH + 12 + btnH + pad

	// Add the window frame to the client size and center the window.
	r := rect{0, 0, scale(clientW), scale(clientH)}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), wsCaption|wsSysMenu, 0)
	winW := r.right - r.left
	winH := r.bottom - r.top
	scrW, _, _ := procGetSystemMetrics.Call(smCxScreen)
	scrH, _, _ := procGetSystemMetrics.Call(smCyScreen)
	x := (int32(scrW) - winW) / 2
	y := (int32(scrH) - winH) / 2

	titlePtr, _ := syscall.UTF16PtrFromString(title)
	p.hwnd, _, _ = procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(classNamePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		wsCaption|wsSysMenu,
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, hInst, 0)

	staticPtr, _ := syscall.UTF16PtrFromString("STATIC")
	emptyPtr, _ := syscall.UTF16PtrFromString("")
	p.label, _, _ = procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(staticPtr)),
		uintptr(unsafe.Pointer(emptyPtr)),
		wsChild|wsVisible|ssNoPrefix,
		uintptr(scale(pad)), uintptr(scale(pad)),
		uintptr(scale(clientW-2*pad)), uintptr(scale(labelH)),
		p.hwnd, 0, hInst, 0)

	barClassPtr, _ := syscall.UTF16PtrFromString("msctls_progress32")
	p.bar, _, _ = procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(barClassPtr)),
		uintptr(unsafe.Pointer(emptyPtr)),
		wsChild|wsVisible,
		uintptr(scale(pad)), uintptr(scale(pad+labelH+10)),
		uintptr(scale(clientW-2*pad)), uintptr(scale(barH)),
		p.hwnd, 0, hInst, 0)
	procSendMessage.Call(p.bar, pbmSetRange32, 0, 1000)

	btnClassPtr, _ := syscall.UTF16PtrFromString("BUTTON")
	btnTextPtr, _ := syscall.UTF16PtrFromString("Abbrechen")
	p.btn, _, _ = procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(btnClassPtr)),
		uintptr(unsafe.Pointer(btnTextPtr)),
		wsChild|wsVisible,
		uintptr(scale(clientW-pad-btnW)), uintptr(scale(pad+labelH+10+barH+12)),
		uintptr(scale(btnW)), uintptr(scale(btnH)),
		p.hwnd, cancelBtnID, hInst, 0)

	// Standard GUI font (Segoe UI) at system size.
	fontPtr, _ := syscall.UTF16PtrFromString("Segoe UI")
	font, _, _ := procCreateFont.Call(uintptr(int32(-int(dpi)*9/72)), 0, 0, 0,
		fwNormal, 0, 0, 0, defaultCharset, 0, 0, cleartype, 0,
		uintptr(unsafe.Pointer(fontPtr)))
	if font != 0 {
		procSendMessage.Call(p.label, wmSetFont, font, 1)
		procSendMessage.Call(p.btn, wmSetFont, font, 1)
	}

	// Set the embedded icon for the title bar/taskbar too.
	if icon := appIcon(); icon != 0 {
		procSendMessage.Call(p.hwnd, wmSetIcon, 0, icon)
		procSendMessage.Call(p.hwnd, wmSetIcon, 1, icon)
	}

	procShowWindow.Call(p.hwnd, swShow)
	procUpdateWindow.Call(p.hwnd)
	procSetForegroundWin.Call(p.hwnd)
}

// requestCancel is called by the GUI thread when cancel/X is clicked.
func (p *progressWin) requestCancel() {
	if p.canceled.CompareAndSwap(false, true) {
		procEnableWindow.Call(p.btn, 0)
		p.SetStatus("Wird abgebrochen ...")
	}
}

// Canceled reports whether the user has canceled the operation.
func (p *progressWin) Canceled() bool {
	return p.canceled.Load()
}

// DisableCancel grays out the cancel button; from here on the operation
// can no longer be canceled (activation phase).
func (p *progressWin) DisableCancel() {
	procEnableWindow.Call(p.btn, 0)
}

// SetStatus sets the text above the progress bar.
func (p *progressWin) SetStatus(text string) {
	t, _ := syscall.UTF16PtrFromString(text)
	procSetWindowText.Call(p.label, uintptr(unsafe.Pointer(t)))
}

// SetProgress updates the progress bar. When the total size is unknown
// (total <= 0) the bar runs in marquee mode.
func (p *progressWin) SetProgress(done, total int64) {
	if total <= 0 {
		if !p.marquee {
			p.marquee = true
			procSetWindowLong.Call(p.bar, gwlStyle, wsChild|wsVisible|pbsMarquee)
			procSendMessage.Call(p.bar, pbmSetMarquee, 1, 50)
		}
		return
	}
	if p.marquee {
		p.marquee = false
		procSendMessage.Call(p.bar, pbmSetMarquee, 0, 0)
		procSetWindowLong.Call(p.bar, gwlStyle, wsChild|wsVisible)
	}
	procSendMessage.Call(p.bar, pbmSetPos, uintptr(done*1000/total), 0)
}

// Close closes the window and waits until the GUI thread has finished.
func (p *progressWin) Close() {
	procPostMessage.Call(p.hwnd, wmAppClose, 0, 0)
	p.wg.Wait()
	activeProgress.CompareAndSwap(p, nil)
}
