//go:build windows

package main

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Custom modal dialogs drawn with the Win32 API. Native MessageBox buttons
// are labeled by Windows in the system language (e.g. "Ja"/"Nein" on a German
// system); these self-drawn dialogs give fixed English button labels
// independent of the OS language. Layout auto-sizes to the message text.

const (
	wsPopup   = 0x80000000
	wsTabstop = 0x00010000
	wsGroup   = 0x00020000

	bsPushButton    = 0x00000000
	bsDefPushButton = 0x00000001

	ssLeft     = 0x00000000
	ssIcon     = 0x00000003
	stmSetIcon = 0x0170

	exDlgModalFrame = 0x00000001
	exTopmost       = 0x00000008

	dtCalcRect   = 0x00000400
	dtWordBreak  = 0x00000010
	dtSingleLine = 0x00000020
	dtNoPrefix   = 0x00000800
	dtExpandTabs = 0x00000040

	idiError    = 32513
	idiQuestion = 32514
	idiWarning  = 32515
	idiInfo     = 32516
)

var (
	procGetDC           = user32.NewProc("GetDC")
	procReleaseDC       = user32.NewProc("ReleaseDC")
	procDrawText        = user32.NewProc("DrawTextW")
	procIsDialogMessage = user32.NewProc("IsDialogMessageW")
	procSetFocus        = user32.NewProc("SetFocus")
	procSelectObject    = gdi32.NewProc("SelectObject")

	dlgClassOnce sync.Once
	dlgClassPtr  *uint16

	// Dialogs are modal and shown sequentially from the main goroutine, so a
	// single active-dialog pointer is enough for the window proc.
	curDlg *dlgState
)

type dialogButton struct {
	text string
	id   int
}

type dlgState struct {
	buttons  []dialogButton
	cancelID int // returned when closed via Esc or the window's X
	result   int
}

func dialogProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		if d := curDlg; d != nil {
			id := int(wParam & 0xffff)
			if id == idCancel { // Esc maps to IDCANCEL via IsDialogMessage
				id = d.cancelID
			}
			for _, b := range d.buttons {
				if b.id == id {
					d.result = id
					procDestroyWindow.Call(hwnd)
					return 0
				}
			}
		}
		return 0
	case wmClose:
		if d := curDlg; d != nil {
			d.result = d.cancelID
		}
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

func registerDialogClass() {
	dlgClassOnce.Do(func() {
		dlgClassPtr, _ = syscall.UTF16PtrFromString("CodePortableDialog")
		hInst, _, _ := procGetModuleHandle.Call(0)
		cursor, _, _ := procLoadCursor.Call(0, 32512) // IDC_ARROW
		wc := wndClassEx{
			cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
			lpfnWndProc:   syscall.NewCallback(dialogProc),
			hInstance:     hInst,
			hCursor:       cursor,
			hbrBackground: colorWindow + 1,
			lpszClassName: dlgClassPtr,
		}
		procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	})
}

func dialogFont(dpi uintptr) uintptr {
	fontPtr, _ := syscall.UTF16PtrFromString("Segoe UI")
	f, _, _ := procCreateFont.Call(uintptr(int32(-int(dpi)*9/72)), 0, 0, 0,
		fwNormal, 0, 0, 0, defaultCharset, 0, 0, cleartype, 0,
		uintptr(unsafe.Pointer(fontPtr)))
	return f
}

// measureText returns the pixel height needed to render text word-wrapped at
// the given width with the given font.
func measureText(text string, width int32, font uintptr) int32 {
	hdc, _, _ := procGetDC.Call(0)
	old, _, _ := procSelectObject.Call(hdc, font)
	p, _ := syscall.UTF16PtrFromString(text)
	r := rect{0, 0, width, 0}
	procDrawText.Call(hdc, uintptr(unsafe.Pointer(p)), ^uintptr(0),
		uintptr(unsafe.Pointer(&r)), dtCalcRect|dtWordBreak|dtNoPrefix|dtExpandTabs)
	procSelectObject.Call(hdc, old)
	procReleaseDC.Call(0, hdc)
	return r.bottom
}

// measureTextWidth returns the pixel width of a single line of text.
func measureTextWidth(text string, font uintptr) int32 {
	hdc, _, _ := procGetDC.Call(0)
	old, _, _ := procSelectObject.Call(hdc, font)
	p, _ := syscall.UTF16PtrFromString(text)
	r := rect{0, 0, 10000, 0}
	procDrawText.Call(hdc, uintptr(unsafe.Pointer(p)), ^uintptr(0),
		uintptr(unsafe.Pointer(&r)), dtCalcRect|dtSingleLine|dtNoPrefix)
	procSelectObject.Call(hdc, old)
	procReleaseDC.Call(0, hdc)
	return r.right
}

// showDialog shows a modal dialog with an icon, a message and the given
// buttons (laid out left-to-right, right-aligned). It blocks until a button
// is clicked and returns its id; Esc or the window's X return cancelID.
func showDialog(title, message string, hIcon uintptr, buttons []dialogButton, defaultID, cancelID int) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	registerDialogClass()
	hInst, _, _ := procGetModuleHandle.Call(0)

	dpi := uintptr(96)
	if procGetDpiForSystem.Find() == nil {
		if d, _, _ := procGetDpiForSystem.Call(); d != 0 {
			dpi = d
		}
	}
	scale := func(v int) int32 { return int32(v * int(dpi) / 96) }
	font := dialogFont(dpi)

	const (
		pad      = 16
		iconSize = 32
		gap      = 14
		btnW     = 84
		btnH     = 26
		btnGap   = 8
		maxTextW = 320 // widest text before it wraps
		minTextW = 150 // keep short messages from being too cramped
	)

	padD := scale(pad)
	iconD := scale(iconSize)
	textX := scale(pad + iconSize + gap)

	// Button widths first - the dialog must be at least as wide as the row.
	widths := make([]int32, len(buttons))
	var btnTotalW int32
	for i, b := range buttons {
		w := measureTextWidth(b.text, font) + scale(24)
		if m := scale(btnW); w < m {
			w = m
		}
		widths[i] = w
		btnTotalW += w
		if i > 0 {
			btnTotalW += scale(btnGap)
		}
	}

	// Text width: widest single line, clamped, so the dialog fits the text
	// instead of using a fixed width.
	var natW int32
	for _, line := range strings.Split(message, "\n") {
		if w := measureTextWidth(line, font); w > natW {
			natW = w
		}
	}
	textW := natW
	if hi := scale(maxTextW); textW > hi {
		textW = hi
	}
	if lo := scale(minTextW); textW < lo {
		textW = lo
	}

	// Client width = the wider of text-based and button-row; then let the
	// text fill whatever width won.
	clientWD := textX + textW + padD
	if bw := padD + btnTotalW + padD; bw > clientWD {
		clientWD = bw
	}
	textW = clientWD - textX - padD

	textH := measureText(message, textW, font)
	contentH := iconD
	if textH > contentH {
		contentH = textH
	}
	btnRowY := padD + contentH + scale(18)
	clientHD := btnRowY + scale(btnH) + padD

	// Frame around the client area, then center on screen.
	style := uintptr(wsPopup | wsCaption | wsSysMenu)
	r := rect{0, 0, clientWD, clientHD}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), style, 0)
	winW := r.right - r.left
	winH := r.bottom - r.top
	scrW, _, _ := procGetSystemMetrics.Call(smCxScreen)
	scrH, _, _ := procGetSystemMetrics.Call(smCyScreen)
	x := (int32(scrW) - winW) / 2
	y := (int32(scrH) - winH) / 2

	titlePtr, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, _ := procCreateWindowEx.Call(exDlgModalFrame|exTopmost,
		uintptr(unsafe.Pointer(dlgClassPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		style, uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, hInst, 0)

	staticPtr, _ := syscall.UTF16PtrFromString("STATIC")
	emptyPtr, _ := syscall.UTF16PtrFromString("")

	// Icon (top-left).
	if hIcon != 0 {
		iconCtl, _, _ := procCreateWindowEx.Call(0,
			uintptr(unsafe.Pointer(staticPtr)), uintptr(unsafe.Pointer(emptyPtr)),
			wsChild|wsVisible|ssIcon,
			uintptr(padD), uintptr(padD), uintptr(iconD), uintptr(iconD),
			hwnd, 0, hInst, 0)
		procSendMessage.Call(iconCtl, stmSetIcon, hIcon, 0)
	}

	// Message text (right of the icon).
	msgPtr, _ := syscall.UTF16PtrFromString(message)
	textCtl, _, _ := procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(staticPtr)), uintptr(unsafe.Pointer(msgPtr)),
		wsChild|wsVisible|ssLeft|ssNoPrefix,
		uintptr(textX), uintptr(padD), uintptr(textW), uintptr(contentH),
		hwnd, 0, hInst, 0)
	procSendMessage.Call(textCtl, wmSetFont, font, 1)

	// Buttons, right-aligned as a group in slice order (widths precomputed).
	btnClassPtr, _ := syscall.UTF16PtrFromString("BUTTON")
	bx := clientWD - padD - btnTotalW
	var defaultCtl uintptr
	for i, b := range buttons {
		bstyle := uintptr(wsChild | wsVisible | wsTabstop | wsGroup | bsPushButton)
		if b.id == defaultID {
			bstyle = wsChild | wsVisible | wsTabstop | wsGroup | bsDefPushButton
		}
		btPtr, _ := syscall.UTF16PtrFromString(b.text)
		ctl, _, _ := procCreateWindowEx.Call(0,
			uintptr(unsafe.Pointer(btnClassPtr)), uintptr(unsafe.Pointer(btPtr)),
			bstyle, uintptr(bx), uintptr(btnRowY), uintptr(widths[i]), uintptr(scale(btnH)),
			hwnd, uintptr(b.id), hInst, 0)
		procSendMessage.Call(ctl, wmSetFont, font, 1)
		if b.id == defaultID {
			defaultCtl = ctl
		}
		bx += widths[i] + scale(btnGap)
	}

	if hIcon != 0 {
		procSendMessage.Call(hwnd, wmSetIcon, 0, hIcon)
		procSendMessage.Call(hwnd, wmSetIcon, 1, hIcon)
	}

	d := &dlgState{buttons: buttons, cancelID: cancelID, result: cancelID}
	curDlg = d

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	procSetForegroundWin.Call(hwnd)
	if defaultCtl != 0 {
		procSetFocus.Call(defaultCtl)
	}

	// Message loop; IsDialogMessage gives Tab/Enter/Esc dialog behavior.
	var m winMsg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if ret, _, _ := procIsDialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&m))); ret == 0 {
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}

	curDlg = nil
	return d.result
}
