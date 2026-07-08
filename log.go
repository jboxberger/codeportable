//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Simple logfile next to the EXE (<ExeName>.log) in the standard format
//
//	2026-07-09 14:23:01.456 [INFO ] message
//
// It logs the normal flow as well as warnings and errors - including the
// original error texts (timeouts, DNS resolution, HTTP status, ...), so
// that it can be traced why a start or update went wrong. Writing is
// "best effort": if the file cannot be created (e.g. a write-protected
// USB stick), the launcher continues without a logfile.
//
// Only messages at or above the configured level (see setLogLevel) are
// written, and the file is created lazily on the first such message - so
// at the default level "error" a cleanly working launcher leaves no
// logfile behind.

const maxLogSize = 512 * 1024 // truncate the logfile on open once it exceeds this

// Severity levels, ascending.
const (
	levelInfo = iota
	levelWarn
	levelError
)

var (
	logPath      string
	logThreshold = levelError // default; overridden by setLogLevel
	logFile      *os.File
	logOpened    bool // file-open attempted (success or failure)
	logMu        sync.Mutex
)

// initLog records where the logfile would go (<ExeName>.log next to the
// EXE). It does not create the file yet - that happens lazily on the first
// message that meets the configured level.
func initLog(exePath string) {
	logPath = strings.TrimSuffix(exePath, filepath.Ext(exePath)) + ".log"
}

// setLogLevel sets the minimum severity that gets written. Unknown values
// leave the default ("error") in place.
func setLogLevel(name string) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "info":
		logThreshold = levelInfo
	case "warn", "warning":
		logThreshold = levelWarn
	case "error":
		logThreshold = levelError
	}
}

func closeLog() {
	if logFile != nil {
		logFile.Close()
	}
}

// writeLog writes a formatted line if level meets the threshold, opening
// (and if needed truncating) the logfile on first use. os.File writes go
// out directly as a syscall (unbuffered), so the lines are fully on disk
// even without closeLog() and even on os.Exit().
func writeLog(level int, levelName, format string, args ...any) {
	if level < logThreshold {
		return
	}

	logMu.Lock()
	defer logMu.Unlock()

	if !logOpened {
		logOpened = true // attempt only once, even if opening fails
		flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
		if fi, err := os.Stat(logPath); err == nil && fi.Size() > maxLogSize {
			flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		}
		if f, err := os.OpenFile(logPath, flags, 0o644); err == nil {
			logFile = f
		}
	}
	if logFile == nil {
		return
	}

	msg := fmt.Sprintf(format, args...)
	// Collapse multi-line messages (e.g. dialog texts) onto one log line.
	msg = strings.ReplaceAll(strings.ReplaceAll(msg, "\r\n", " | "), "\n", " | ")
	line := fmt.Sprintf("%s [%-5s] %s\r\n", time.Now().Format("2006-01-02 15:04:05.000"), levelName, msg)
	logFile.WriteString(line)
}

func logInfo(format string, args ...any)  { writeLog(levelInfo, "INFO", format, args...) }
func logWarn(format string, args ...any)  { writeLog(levelWarn, "WARN", format, args...) }
func logError(format string, args ...any) { writeLog(levelError, "ERROR", format, args...) }
