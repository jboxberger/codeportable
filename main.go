//go:build windows

// CodePortable ist ein abhängigkeitsfreier Starter für Visual Studio Code
// im Portable-Modus. Eine einzige Standalone-EXE ohne DLLs; die gesamte
// Oberfläche (Fortschrittsfenster, Rückfragen) läuft über die Win32-API.
//
// Verzeichnislayout neben der EXE:
//
//	CodePortable.exe
//	config.ini           <- Download-URL, Anzahl aufbewahrter Versionen
//	current\             <- aktive Code-Instanz (Code.exe, ...)
//	current\data\        <- Portable-Datenordner (Einstellungen, Extensions)
//	old\<Version>\       <- frühere Versionen samt damaligem data-Stand
//	install.tmp\         <- Staging während Download/Entpacken, danach gelöscht
//
// Beim Update wird die neue Version vollständig im Staging aufgebaut
// (inklusive einer Kopie der Benutzerdaten), dann wandert der bisherige
// current-Ordner nach old\<Version> (Backup/Historie) und der neue Stand
// wird nach current umbenannt. Revert = current wegbenennen und den
// gewünschten old\<Version>-Ordner wieder nach current umbenennen.
// In old\ werden nur die letzten keepversions Stände behalten (config.ini,
// Standard 5); ältere werden gelöscht.
//
// Crash-Sicherheit: Download und Entpacken passieren vollständig im
// Staging-Ordner install.tmp (wird bei jedem Start aufgeräumt). Der
// Wechsel selbst besteht nur aus zwei Rename-Operationen; das Umbenennen
// des alten current dient zugleich als Lock-Test (schlägt fehl, solange
// Code läuft). Benutzerdaten werden nie gelöscht - schlimmstenfalls liegen
// sie im jüngsten old\<Version>-Ordner.
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

	// Reste eines abgebrochenen/abgestürzten Laufs entsorgen.
	os.RemoveAll(stagingDir)

	cfg := loadConfig(filepath.Join(baseDir, "config.ini"))

	if _, err := os.Stat(codeExe); err != nil {
		// Erststart oder unvollständige Installation: (neu) herunterladen.
		info, err := fetchLatest(cfg.APIURL)
		if err != nil {
			fatal("Versionsabfrage fehlgeschlagen (keine Internetverbindung?):\n\n" + err.Error())
		}
		if !install(cfg, info, stagingDir, baseDir, currentDir) {
			return // vom Benutzer abgebrochen - es gibt nichts zu starten
		}
	} else {
		checkForUpdate(cfg, stagingDir, baseDir, currentDir, codeExe)
	}

	// Portable-Modus: der data-Ordner neben Code.exe macht die Installation portabel.
	if err := os.MkdirAll(filepath.Join(dataDir, "tmp"), 0o755); err != nil {
		fatal("Data-Ordner konnte nicht erstellt werden: " + err.Error())
	}

	waitForOtherUpdater()
	launch(codeExe)
}

// waitForOtherUpdater warnt, wenn gerade eine ANDERE Code-Installation auf
// diesem Rechner aktualisiert wird (Inno-Setup-Mutex "vscode-updating",
// maschinenweit). Code prüft diesen Mutex beim Start selbst, wartet 30
// Sekunden und beendet sich dann kommentarlos - für den Anwender sähe es
// so aus, als würde der Start einfach nicht funktionieren. Daher hier eine
// klare Rückfrage. Diese Sperre hat nichts mit dem Update-Dialog dieses
// Launchers zu tun; sie kommt von einer fremden Code-Installation.
func waitForOtherUpdater() {
	for isMutexHeld("vscode-updating") {
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
			return // trotzdem starten - Code wartet selbst auf die Sperre
		default:
			os.Exit(0)
		}
	}
}

// checkForUpdate prüft bei jedem Start, ob eine neuere Version verfügbar ist,
// und fragt den Benutzer, ob aktualisiert werden soll. Die installierte
// Version wird direkt aus der Versionsressource der Code.exe gelesen.
func checkForUpdate(cfg *config, stagingDir, baseDir, currentDir, codeExe string) {
	info, err := fetchLatest(cfg.APIURL)
	if err != nil {
		// Offline oder Server nicht erreichbar: einfach normal starten.
		return
	}
	current := fileProductVersion(codeExe)
	if current == "" || !isNewer(info.ProductVersion, current) {
		return
	}

	msg := fmt.Sprintf("Installiert:\t%s\nVerfügbar:\t%s\n\nJetzt aktualisieren?",
		current, info.ProductVersion)
	if !askYesNo("Update verfügbar", msg) {
		return
	}
	// Bei Abbruch bleibt die alte Version unangetastet und startet normal.
	install(cfg, info, stagingDir, baseDir, currentDir)
}

// install lädt die angegebene Version in den Staging-Ordner, entpackt sie
// dort, übernimmt eine Kopie der Benutzerdaten und aktiviert sie dann:
// Der bisherige current-Ordner wandert nach old\<Version>, der neue Stand
// nach current. Liefert false, wenn der Benutzer abgebrochen hat - dann
// ist alles aufgeräumt und current unverändert.
func install(cfg *config, info *updateInfo, stagingDir, baseDir, currentDir string) bool {
	os.RemoveAll(stagingDir)
	newCurrent := filepath.Join(stagingDir, "current")
	if err := os.MkdirAll(newCurrent, 0o755); err != nil {
		fatal("Staging-Ordner konnte nicht erstellt werden:\n\n" + err.Error())
	}

	win := newProgressWin(appTitle)
	fail := func(msg string) {
		win.Close()
		os.RemoveAll(stagingDir)
		fatal(msg)
	}

	// Phase 1: Download in den Staging-Ordner (abbrechbar).
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
		win.Close()
		os.RemoveAll(stagingDir)
		return false
	}
	if err != nil {
		fail("Download fehlgeschlagen:\n\n" + err.Error())
	}

	// Phase 2: Entpacken in den Staging-Ordner (abbrechbar).
	win.SetStatus("Code " + info.ProductVersion + " wird entpackt ...")
	err = extractZip(zipPath, newCurrent, win.Canceled, func(done, total int) {
		win.SetProgress(int64(done), int64(total))
	})
	if err == nil && win.Canceled() {
		err = errCanceled
	}
	if err == errCanceled {
		win.Close()
		os.RemoveAll(stagingDir)
		return false
	}
	if err != nil {
		fail("Entpacken fehlgeschlagen:\n\n" + err.Error())
	}
	os.Remove(zipPath)

	// Phase 3: Aktivieren - ab hier nicht mehr abbrechbar.
	win.DisableCancel()
	win.SetStatus("Code " + info.ProductVersion + " wird installiert ...")
	win.SetProgress(0, 0) // Marquee: Dauer unbekannt

	// Benutzerdaten der bisherigen Installation in den neuen Stand
	// KOPIEREN - das Original bleibt vollständig im Versionsarchiv.
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

	// Bisherigen current-Ordner nach old\<Version> verschieben statt löschen
	// (Backup/Historie). Das Umbenennen ist zugleich der Lock-Test: Es
	// schlägt fehl, solange Code aus diesem Ordner noch läuft.
	oldDir := filepath.Join(baseDir, "old")
	if _, err := os.Stat(currentDir); err == nil {
		if err := os.MkdirAll(oldDir, 0o755); err != nil {
			fail("old-Ordner konnte nicht erstellt werden:\n\n" + err.Error())
		}
		oldVersion := fileProductVersion(filepath.Join(currentDir, "Code.exe"))
		if err := renameRetry(currentDir, archivePath(oldDir, oldVersion)); err != nil {
			fail("Update nicht möglich - läuft Code noch?\n\n" + err.Error())
		}
	}

	// Neuen Stand aktivieren (einzelnes Rename, gleiches Laufwerk).
	if err := renameRetry(newCurrent, currentDir); err != nil {
		fail("Neue Version konnte nicht aktiviert werden:\n\n" + err.Error())
	}

	win.SetStatus("Alte Versionen werden aufgeräumt ...")
	pruneOldVersions(oldDir, cfg.KeepVersions)

	win.Close()
	os.RemoveAll(stagingDir)
	return true
}

// renameRetry benennt um und wiederholt es bei transienten Fehlern. Unter
// Windows scheitert das Umbenennen eines Ordners, solange ein anderer
// Prozess eine Datei darin geöffnet hält - etwa der Virenscanner, der die
// gerade entpackte Code.exe prüft. Ein laufendes Code hält seine Dateien
// dauerhaft offen, daher wird nach kurzer Zeit endgültig aufgegeben.
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

// archivePath liefert einen freien Ordnernamen für eine frühere Version,
// z. B. old\1.120.0 (bei Kollision old\1.120.0-2 usw.). Ohne ermittelbare
// Version wird "backup" verwendet.
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

// pruneOldVersions behält in old\ nur die neuesten keep Versionsordner und
// löscht die älteren. Sortiert wird nach Versionsnummer; Ordner ohne
// erkennbare Version (z. B. "backup") gelten als die ältesten.
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
	// Neueste zuerst.
	sort.Slice(names, func(i, j int) bool { return isNewer(names[i], names[j]) })
	for _, name := range names[min(keep, len(names)):] {
		os.RemoveAll(filepath.Join(oldDir, name))
	}
}

// isNewer vergleicht zwei Versionen der Form "1.101.2" numerisch.
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

// launch startet Code.exe losgelöst vom Launcher und reicht alle
// Kommandozeilenargumente (z. B. zu öffnende Dateien) durch.
//
// Der Portable-Modus braucht weder Parameter noch ein bestimmtes Working
// Directory: Code erkennt ihn allein am data-Ordner neben seiner EXE.
// Das Working Directory wird bewusst vom Launcher geerbt (nicht auf bin\
// gesetzt), damit relative Pfad-Argumente wie "CodePortable.exe .\datei.txt"
// aus Sicht des Aufrufers aufgelöst werden.
func launch(codeExe string) {
	cmd := exec.Command(codeExe)
	cmd.Args = append(cmd.Args, os.Args[1:]...)
	if err := cmd.Start(); err != nil {
		fatal("Code konnte nicht gestartet werden:\n\n" + err.Error())
	}
}

// fatal zeigt eine Fehlermeldung als Dialog an und beendet den Launcher.
func fatal(msg string) {
	messageBox(appTitle, msg, mbOK|mbIconError)
	os.Exit(1)
}
