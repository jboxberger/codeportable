//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Fallback für die Versionserkennung: Die Produktversion wird direkt aus
// der Versionsressource der Code.exe gelesen (version.dll ist eine
// Windows-Systembibliothek). Damit ist die Erkennung unabhängig vom
// Verzeichnislayout des Code-Archivs.

var (
	versionDLL                 = syscall.NewLazyDLL("version.dll")
	procGetFileVersionInfoSize = versionDLL.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfo     = versionDLL.NewProc("GetFileVersionInfoW")
	procVerQueryValue          = versionDLL.NewProc("VerQueryValueW")
)

type vsFixedFileInfo struct {
	Signature        uint32
	StrucVersion     uint32
	FileVersionMS    uint32
	FileVersionLS    uint32
	ProductVersionMS uint32
	ProductVersionLS uint32
	FileFlagsMask    uint32
	FileFlags        uint32
	FileOS           uint32
	FileType         uint32
	FileSubtype      uint32
	FileDateMS       uint32
	FileDateLS       uint32
}

// fileProductVersion liest die Produktversion (z. B. "1.128.0") aus der
// Versionsressource einer EXE. Liefert "" bei Fehlern.
func fileProductVersion(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	var handle uint32
	size, _, _ := procGetFileVersionInfoSize.Call(
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&handle)))
	if size == 0 {
		return ""
	}
	buf := make([]byte, size)
	ok, _, _ := procGetFileVersionInfo.Call(
		uintptr(unsafe.Pointer(p)), 0, size, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		return ""
	}
	sub, _ := syscall.UTF16PtrFromString(`\`)
	var fixed *vsFixedFileInfo
	var length uint32
	ok, _, _ = procVerQueryValue.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(sub)),
		uintptr(unsafe.Pointer(&fixed)),
		uintptr(unsafe.Pointer(&length)))
	if ok == 0 || fixed == nil || length == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d",
		fixed.ProductVersionMS>>16, fixed.ProductVersionMS&0xffff,
		fixed.ProductVersionLS>>16)
}
