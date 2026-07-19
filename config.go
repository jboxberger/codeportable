//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultAPIURL returns metadata (download URL, version, SHA-256) for the
// latest portable version (win32-x64-archive = ZIP) on the stable channel.
const defaultAPIURL = "https://update.code.visualstudio.com/api/update/win32-x64-archive/stable/latest"

const defaultKeepVersions = 5

const defaultLogLevel = "warn"

const configTemplate = `; =====================================================================
; Code Portable Launcher - Configuration
; =====================================================================
;
; apiurl
; ------
; API endpoint the launcher queries for metadata about the latest
; Code version (download URL, version number, SHA-256 hash).
; The response must be JSON containing the fields "url",
; "productVersion" and "sha256hash".
;
; Default (Code Stable, 64-bit Windows, ZIP/portable):
;   https://update.code.visualstudio.com/api/update/win32-x64-archive/stable/latest
;
; Older versions (like 1.120.0):
;   https://update.code.visualstudio.com/{version}/win32-x64-archive/stable
;
; Should Microsoft ever change this path, simply enter the new one
; here. The downloaded file must be a ZIP that contains Code.exe at
; its top level (the "Archive" variant from
; https://code.visualstudio.com/download).
;
; keepversions
; ------------
; How many previous versions to keep as a backup in the "old" folder.
; On every update the active "current" folder is moved to
; "old\<version>" (including its data folder), so any version can be
; restored quickly. Once more than this many folders exist in "old",
; the oldest ones are deleted.
;
; Default: %d   (0 = keep none, delete every previous version)
;
; loglevel
; --------
; Minimum severity written to the logfile next to the EXE
; (<ExeName>.log). The logfile is created only once a message of at
; least this level occurs, so at the default level a cleanly working
; launcher writes no logfile at all.
;
;   error   only failures
;   warn    failures + warnings, e.g. update check offline (default)
;   info    full trace of every step (use for debugging)
;
; Default: %s
;
; =====================================================================

[update]
apiurl = %s
keepversions = %d
loglevel = %s
`

type config struct {
	APIURL       string
	KeepVersions int
	LogLevel     string
}

// loadConfig reads the config.ini next to the EXE. If it does not exist
// yet (first run), it is created with default values and an explanatory
// comment.
func loadConfig(path string) *config {
	cfg := &config{APIURL: defaultAPIURL, KeepVersions: defaultKeepVersions, LogLevel: defaultLogLevel}

	raw, err := os.ReadFile(path)
	if err != nil {
		content := fmt.Sprintf(configTemplate, defaultKeepVersions, defaultLogLevel, defaultAPIURL, defaultKeepVersions, defaultLogLevel)
		if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
			fatal("config.ini could not be created:\n" + writeErr.Error())
		}
		return cfg
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "apiurl":
			if value != "" {
				cfg.APIURL = value
			}
		case "keepversions":
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				cfg.KeepVersions = n
			}
		case "loglevel":
			if value != "" {
				cfg.LogLevel = value
			}
		}
	}
	return cfg
}
