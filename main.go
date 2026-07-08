//go:build windows

// CodePortable is a dependency-free launcher for Visual Studio Code in
// portable mode. A single standalone EXE without DLLs; the entire
// interface (progress window, prompts) runs through the Win32 API.
//
// Directory layout next to the EXE:
//
//	CodePortable.exe
//	config.ini           <- download URL, number of versions to keep
//	current\             <- active Code instance (Code.exe, ...)
//	current\data\        <- portable data folder (settings, extensions)
//	old\<version>\       <- previous versions with their data at the time
//	install.tmp\         <- staging during download/extract, deleted after
//
// On an update the new version is fully assembled in staging (including a
// copy of the user data), then the current folder is moved to
// old\<version> (backup/history) and the new state is renamed to current.
// Revert = rename current away and rename the desired old\<version>
// folder back to current. Only the most recent keepversions states are
// kept in old\ (config.ini, default 5); older ones are deleted.
//
// Crash safety: download and extraction happen entirely in the staging
// folder install.tmp (cleaned up on every start). The switch itself
// consists of only two rename operations; renaming the old current also
// serves as a lock test (it fails while Code is running). User data is
// never deleted - at worst it sits in the most recent old\<version>
// folder.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	appTitle    = "Code Portable"
	stagingName = "install.tmp"
)

func main() {
	exePath, err := os.Executable()
	if err != nil {
		fatal("Eigener Pfad konnte nicht ermittelt werden: " + err.Error())
	}
	baseDir := filepath.Dir(exePath)
	currentDir := filepath.Join(baseDir, "current")
	dataDir := filepath.Join(currentDir, "data")
	codeExe := filepath.Join(currentDir, "Code.exe")
	stagingDir := filepath.Join(baseDir, stagingName)

	initLog(exePath)
	defer closeLog()

	// Discard leftovers from an aborted/crashed run.
	os.RemoveAll(stagingDir)

	// Load config first, then apply the log level before anything is logged.
	cfg := loadConfig(filepath.Join(baseDir, "config.ini"))
	setLogLevel(cfg.LogLevel)
	logInfo("---- CodePortable started (args: %v) ----", os.Args[1:])
	logInfo("configuration: apiurl=%s keepversions=%d loglevel=%s", cfg.APIURL, cfg.KeepVersions, cfg.LogLevel)

	if _, err := os.Stat(codeExe); err != nil {
		// First start or incomplete installation: (re)download.
		logInfo("no installation at %s - starting first-time install", codeExe)
		info, err := fetchLatest(cfg.APIURL)
		if err != nil {
			logError("version query failed: %v", err)
			fatal("Versionsabfrage fehlgeschlagen (keine Internetverbindung?):\n\n" + err.Error())
		}
		if !install(cfg, info, stagingDir, baseDir, currentDir) {
			logInfo("first-time install canceled by user")
			return // canceled by user - there is nothing to start
		}
	} else {
		checkForUpdate(cfg, stagingDir, baseDir, currentDir, codeExe)
	}

	// Portable mode: the data folder next to Code.exe makes the installation portable.
	if err := os.MkdirAll(filepath.Join(dataDir, "tmp"), 0o755); err != nil {
		fatal("Data-Ordner konnte nicht erstellt werden: " + err.Error())
	}

	waitForOtherUpdater()
	launch(codeExe)
}

// waitForOtherUpdater warns when ANOTHER Code installation on this
// machine is currently updating (Inno Setup mutex "vscode-updating",
// machine-wide). Code checks this mutex itself on startup, waits 30
// seconds and then exits without comment - to the user it would look as
// if the start simply does not work. Hence a clear prompt here. This lock
// has nothing to do with this launcher's update dialog; it comes from a
// foreign Code installation.
func waitForOtherUpdater() {
	logged := false
	for isMutexHeld("vscode-updating") {
		if !logged {
			logWarn("system-wide lock 'vscode-updating' active - foreign Code installation is updating; prompting")
			logged = true
		}
		ret := messageBox(appTitle,
			"Unabhängig von diesem Launcher: Eine andere Code-Installation auf "+
				"diesem Rechner (z. B. das normal installierte Code) führt gerade "+
				"ihr eigenes Update durch und hält dabei eine systemweite Sperre. "+
				"Code startet grundsätzlich nicht, solange diese Sperre besteht.\n\n"+
				"Abhilfe: alle Fenster der anderen Code-Installation schließen, "+
				"damit deren Update durchlaufen kann.\n\n"+
				"Wiederholen:\tSperre erneut prüfen\n"+
				"Weiter:\ttrotzdem starten (Code wartet selbst bis zu 30 Sekunden)\n"+
				"Abbrechen:\tbeenden ohne Start",
			mbCancelTryContinue|mbDefButton2|mbIconWarning)
		switch ret {
		case idTryAgain:
			continue
		case idContinue:
			logWarn("user: start anyway despite active lock")
			return // start anyway - Code waits for the lock itself
		default:
			logInfo("user: start canceled because of active lock")
			os.Exit(0)
		}
	}
}

// checkForUpdate checks on every start whether a newer version is
// available and asks the user whether to update. The installed version is
// read directly from Code.exe's version resource.
func checkForUpdate(cfg *config, stagingDir, baseDir, currentDir, codeExe string) {
	logInfo("update check against %s", cfg.APIURL)
	info, err := fetchLatest(cfg.APIURL)
	if err != nil {
		// Offline or server unreachable: just start normally.
		// (err holds the specific reason: timeout, DNS error, HTTP status.)
		logWarn("update check failed, starting without check: %v", err)
		return
	}
	current := fileProductVersion(codeExe)
	if current == "" || !isNewer(info.ProductVersion, current) {
		logInfo("no update needed (installed: %q, available: %s)", current, info.ProductVersion)
		return
	}
	logInfo("update available: %s -> %s", current, info.ProductVersion)

	msg := fmt.Sprintf("Installiert:\t%s\nVerfügbar:\t%s\n\nJetzt aktualisieren?",
		current, info.ProductVersion)
	if !askYesNo("Update verfügbar", msg) {
		logInfo("update declined by user")
		return
	}
	// On cancel the old version stays untouched and starts normally.
	install(cfg, info, stagingDir, baseDir, currentDir)
}

// install downloads the given version into the staging folder, extracts
// it there, takes over a copy of the user data and then activates it: the
// current folder is moved to old\<version>, the new state to current.
// Returns false if the user canceled - in that case everything is cleaned
// up and current is unchanged.
func install(cfg *config, info *updateInfo, stagingDir, baseDir, currentDir string) bool {
	os.RemoveAll(stagingDir)
	newCurrent := filepath.Join(stagingDir, "current")
	if err := os.MkdirAll(newCurrent, 0o755); err != nil {
		fatal("Staging-Ordner konnte nicht erstellt werden:\n\n" + err.Error())
	}

	logInfo("installation started: version %s from %s", info.ProductVersion, info.URL)

	win := newProgressWin(appTitle)
	fail := func(msg string) {
		win.Close()
		os.RemoveAll(stagingDir)
		fatal(msg)
	}

	// Phase 1: download into the staging folder (cancelable).
	statusPrefix := "Code " + info.ProductVersion + " wird heruntergeladen ..."
	win.SetStatus(statusPrefix)
	zipPath := filepath.Join(stagingDir, "code.zip")
	err := downloadZip(info, zipPath, win.Canceled, func(done, total int64) {
		win.SetProgress(done, total)
		if win.Canceled() {
			return
		}
		if total > 0 {
			win.SetStatus(fmt.Sprintf("%s  %.1f von %.1f MB", statusPrefix,
				float64(done)/1024/1024, float64(total)/1024/1024))
		} else {
			win.SetStatus(fmt.Sprintf("%s  %.1f MB", statusPrefix, float64(done)/1024/1024))
		}
	})
	if err == nil && win.Canceled() {
		err = errCanceled
	}
	if err == errCanceled {
		logInfo("download canceled by user")
		win.Close()
		os.RemoveAll(stagingDir)
		return false
	}
	if err != nil {
		logError("download failed: %v", err)
		fail("Download fehlgeschlagen:\n\n" + err.Error())
	}
	logInfo("download finished and SHA-256 verified")

	// Phase 2: extract into the staging folder (cancelable).
	win.SetStatus("Code " + info.ProductVersion + " wird entpackt ...")
	err = extractZip(zipPath, newCurrent, win.Canceled, func(done, total int) {
		win.SetProgress(int64(done), int64(total))
	})
	if err == nil && win.Canceled() {
		err = errCanceled
	}
	if err == errCanceled {
		logInfo("extraction canceled by user")
		win.Close()
		os.RemoveAll(stagingDir)
		return false
	}
	if err != nil {
		logError("extraction failed: %v", err)
		fail("Entpacken fehlgeschlagen:\n\n" + err.Error())
	}
	logInfo("extraction finished")
	os.Remove(zipPath)

	// Phase 3: activate - no longer cancelable from here on.
	win.DisableCancel()
	win.SetStatus("Code " + info.ProductVersion + " wird installiert ...")
	win.SetProgress(0, 0) // Marquee: duration unknown

	// COPY the user data of the previous installation into the new state
	// - the original stays fully in the version archive.
	oldData := filepath.Join(currentDir, "data")
	newData := filepath.Join(newCurrent, "data")
	if _, err := os.Stat(oldData); err == nil {
		win.SetStatus("Benutzerdaten werden übernommen ...")
		if err := copyDir(oldData, newData); err != nil {
			fail("Benutzerdaten konnten nicht übernommen werden:\n\n" + err.Error())
		}
		win.SetStatus("Code " + info.ProductVersion + " wird installiert ...")
	} else if err := os.MkdirAll(newData, 0o755); err != nil {
		fail("Data-Ordner konnte nicht erstellt werden:\n\n" + err.Error())
	}

	// Move the current folder to old\<version> instead of deleting it
	// (backup/history). The rename is also the lock test: it fails while
	// Code is still running from this folder.
	oldDir := filepath.Join(baseDir, "old")
	if _, err := os.Stat(currentDir); err == nil {
		if err := os.MkdirAll(oldDir, 0o755); err != nil {
			fail("old-Ordner konnte nicht erstellt werden:\n\n" + err.Error())
		}
		oldVersion := fileProductVersion(filepath.Join(currentDir, "Code.exe"))
		archive := archivePath(oldDir, oldVersion)
		if err := renameRetry(currentDir, archive); err != nil {
			logError("archiving the old version failed (is Code still running?): %v", err)
			fail("Update nicht möglich - läuft Code noch?\n\n" + err.Error())
		}
		logInfo("old version %q archived to %s", oldVersion, filepath.Base(archive))
	}

	// Activate the new state (single rename, same drive).
	if err := renameRetry(newCurrent, currentDir); err != nil {
		logError("activating the new version failed: %v", err)
		fail("Neue Version konnte nicht aktiviert werden:\n\n" + err.Error())
	}

	win.SetStatus("Alte Versionen werden aufgeräumt ...")
	pruneOldVersions(oldDir, cfg.KeepVersions)

	logInfo("installation finished: version %s active", info.ProductVersion)
	win.Close()
	os.RemoveAll(stagingDir)
	return true
}

// renameRetry renames and retries on transient errors. On Windows,
// renaming a folder fails while another process holds a file inside it
// open - such as the virus scanner checking the freshly extracted
// Code.exe. A running Code keeps its files open permanently, so after a
// short while it gives up for good.
func renameRetry(from, to string) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return err
}

// archivePath returns a free folder name for a previous version, e.g.
// old\1.120.0 (on collision old\1.120.0-2 etc.). If no version can be
// determined, "backup" is used.
func archivePath(oldDir, version string) string {
	if version == "" {
		version = "backup"
	}
	base := filepath.Join(oldDir, version)
	path := base
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
		path = fmt.Sprintf("%s-%d", base, i)
	}
}

// pruneOldVersions keeps only the newest keep version folders in old\ and
// deletes the older ones. Sorting is by version number; folders without a
// recognizable version (e.g. "backup") count as the oldest.
func pruneOldVersions(oldDir string, keep int) {
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	// Newest first.
	sort.Slice(names, func(i, j int) bool { return isNewer(names[i], names[j]) })
	for _, name := range names[min(keep, len(names)):] {
		os.RemoveAll(filepath.Join(oldDir, name))
	}
}

// isNewer compares two versions of the form "1.101.2" numerically.
func isNewer(latest, current string) bool {
	l := strings.Split(latest, ".")
	c := strings.Split(current, ".")
	for i := 0; i < len(l) || i < len(c); i++ {
		lv, cv := 0, 0
		if i < len(l) {
			lv, _ = strconv.Atoi(l[i])
		}
		if i < len(c) {
			cv, _ = strconv.Atoi(c[i])
		}
		if lv != cv {
			return lv > cv
		}
	}
	return false
}

// launch starts Code.exe detached from the launcher and passes through
// all command-line arguments (e.g. files to open).
//
// Portable mode needs neither parameters nor a particular working
// directory: Code recognizes it solely by the data folder next to its
// EXE. The working directory is deliberately inherited from the launcher
// (not set to bin\), so that relative path arguments like
// "CodePortable.exe .\file.txt" are resolved from the caller's point of
// view.
func launch(codeExe string) {
	cmd := exec.Command(codeExe)
	cmd.Args = append(cmd.Args, os.Args[1:]...)
	if err := cmd.Start(); err != nil {
		logError("starting %s failed: %v", codeExe, err)
		fatal("Code konnte nicht gestartet werden:\n\n" + err.Error())
	}
	logInfo("Code started: %s", codeExe)
}

// fatal logs the message, shows it as a dialog and terminates the
// launcher. os.Exit() runs no defer functions; the logfile is written
// unbuffered though, so the last line is already on disk.
func fatal(msg string) {
	logError("ABORT: %s", msg)
	messageBox(appTitle, msg, mbOK|mbIconError)
	os.Exit(1)
}
