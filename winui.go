//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Rückfragen und Fehlermeldungen laufen über native Windows-Dialoge
// (MessageBoxW/MessageBoxIndirectW). Es werden nur Windows-System-
// bibliotheken benutzt, die immer vorhanden sind - keine externen
// Abhängigkeiten.

const (
	mbOK                = 0x00000000
	mbYesNo             = 0x00000004
	mbCancelTryContinue = 0x00000006
	mbIconError         = 0x00000010
	mbIconQuestion      = 0x00000020
	mbIconWarning       = 0x00000030
	mbUserIcon          = 0x00000080
	mbDefButton2        = 0x00000100
	mbTopmost           = 0x00040000

	idYes      = 6
	idTryAgain = 10
	idContinue = 11
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procMessageBox         = user32.NewProc("MessageBoxW")
	procMessageBoxIndirect = user32.NewProc("MessageBoxIndirectW")
	procOpenMutex          = kernel32.NewProc("OpenMutexW")
)

// isMutexHeld prüft, ob ein benannter Windows-Mutex existiert (d. h. von
// irgendeinem Prozess gehalten wird).
func isMutexHeld(name string) bool {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	const synchronize = 0x00100000
	h, _, _ := procOpenMutex.Call(synchronize, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return false
	}
	syscall.CloseHandle(syscall.Handle(h))
	return true
}

// msgBoxParams entspricht MSGBOXPARAMSW; erlaubt eine MessageBox mit
// eigenem Icon aus den EXE-Ressourcen (MB_USERICON).
type msgBoxParams struct {
	cbSize             uint32
	hwndOwner          uintptr
	hInstance          uintptr
	lpszText           uintptr
	lpszCaption        uintptr
	dwStyle            uint32
	lpszIcon           uintptr
	dwContextHelpID    uintptr
	lpfnMsgBoxCallback uintptr
	dwLanguageID       uint32
}

// messageBox zeigt einen nativen Windows-Dialog an und liefert die
// gedrückte Schaltfläche zurück (z. B. idYes).
func messageBox(title, text string, flags uint32) int {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(title)
	ret, _, _ := procMessageBox.Call(0,
		uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(c)),
		uintptr(flags|mbTopmost))
	return int(ret)
}

// askYesNo zeigt eine Ja/Nein-Rückfrage mit dem Anwendungs-Icon statt des
// Standard-Fragezeichens; Fallback ist die klassische Frage-MessageBox.
func askYesNo(title, text string) bool {
	if iconID := appIconID(); iconID != 0 {
		t, _ := syscall.UTF16PtrFromString(text)
		c, _ := syscall.UTF16PtrFromString(title)
		hInst, _, _ := procGetModuleHandle.Call(0)
		params := msgBoxParams{
			cbSize:      uint32(unsafe.Sizeof(msgBoxParams{})),
			hInstance:   hInst,
			lpszText:    uintptr(unsafe.Pointer(t)),
			lpszCaption: uintptr(unsafe.Pointer(c)),
			dwStyle:     mbYesNo | mbUserIcon | mbTopmost,
			lpszIcon:    iconID,
		}
		if ret, _, _ := procMessageBoxIndirect.Call(uintptr(unsafe.Pointer(&params))); ret != 0 {
			return int(ret) == idYes
		}
	}
	return messageBox(title, text, mbYesNo|mbIconQuestion) == idYes
}
