//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Fallback for version detection: the product version is read directly
// from Code.exe's version resource (version.dll is a Windows system
// library). This makes detection independent of the Code archive's
// directory layout.

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

// fileProductVersion reads the product version (e.g. "1.128.0") from an
// EXE's version resource. Returns "" on errors.
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
