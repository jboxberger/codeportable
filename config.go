//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultAPIURL liefert Metadaten (Download-URL, Version, SHA-256) zur
// aktuellsten Portable-Version (win32-x64-archive = ZIP) im Stable-Kanal.
const defaultAPIURL = "https://update.code.visualstudio.com/api/update/win32-x64-archive/stable/latest"

const defaultKeepVersions = 5

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
; =====================================================================

[update]
apiurl = %s
keepversions = %d
`

type config struct {
	APIURL       string
	KeepVersions int
}

// loadConfig liest die config.ini neben der EXE ein. Existiert sie noch
// nicht (erster Lauf), wird sie mit Standardwerten und Erklärkommentar
// angelegt.
func loadConfig(path string) *config {
	cfg := &config{APIURL: defaultAPIURL, KeepVersions: defaultKeepVersions}

	raw, err := os.ReadFile(path)
	if err != nil {
		content := fmt.Sprintf(configTemplate, defaultKeepVersions, defaultAPIURL, defaultKeepVersions)
		if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
			fatal("config.ini konnte nicht angelegt werden:\n" + writeErr.Error())
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
		}
	}
	return cfg
}
