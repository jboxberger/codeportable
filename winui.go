//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// User prompts and error messages use self-drawn modal dialogs (see
// dialog.go) so the button labels stay English regardless of the Windows
// system language. Only always-present Windows system libraries are used -
// no external dependencies.

// Button ids returned by the dialogs.
const (
	idOk       = 1
	idCancel   = 2
	idYes      = 6
	idNo       = 7
	idTryAgain = 10
	idContinue = 11
)

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	procOpenMutex = kernel32.NewProc("OpenMutexW")
)

// errorDialog shows a modal error dialog with a single OK button.
func errorDialog(title, text string) {
	icon, _, _ := procLoadIcon.Call(0, idiError)
	showDialog(title, text, icon, []dialogButton{{"OK", idOk}}, idOk, idOk)
}

// askYesNo shows a Yes/No prompt with the application icon and returns true
// when Yes is chosen.
func askYesNo(title, text string) bool {
	icon := appIcon()
	if icon == 0 {
		icon, _, _ = procLoadIcon.Call(0, idiInfo)
	}
	return showDialog(title, text, icon,
		[]dialogButton{{"Yes", idYes}, {"No", idNo}}, idYes, idNo) == idYes
}

// askRetryContinueCancel shows a warning dialog with Try Again / Continue /
// Cancel buttons and returns the clicked button id. Default is Continue;
// Esc / the window's X map to Cancel.
func askRetryContinueCancel(title, text string) int {
	icon, _, _ := procLoadIcon.Call(0, idiWarning)
	return showDialog(title, text, icon,
		[]dialogButton{{"Try Again", idTryAgain}, {"Continue", idContinue}, {"Cancel", idCancel}},
		idContinue, idCancel)
}

// isMutexHeld reports whether a named Windows mutex exists (i.e. is held by
// some process).
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
