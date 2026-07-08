//go:build windows

package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// errCanceled signalisiert einen Abbruch durch den Benutzer.
var errCanceled = errors.New("vom Benutzer abgebrochen")

type updateInfo struct {
	URL            string `json:"url"`
	ProductVersion string `json:"productVersion"`
	SHA256Hash     string `json:"sha256hash"`
}

// fetchLatest fragt die aktuellste verfügbare Code-Version beim in der
// config.ini hinterlegten API-Endpunkt ab.
func fetchLatest(apiURL string) (*updateInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Update-Server antwortete mit HTTP %d", resp.StatusCode)
	}
	var info updateInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	if info.URL == "" || info.ProductVersion == "" {
		return nil, fmt.Errorf("unerwartete Antwort des Update-Servers")
	}
	return &info, nil
}

// downloadZip lädt die ZIP nach destPath, meldet den Fortschritt über
// progress(done, total), prüft die SHA-256-Prüfsumme und bricht ab, sobald
// canceled() true liefert.
func downloadZip(info *updateInfo, destPath string, canceled func() bool, progress func(done, total int64)) error {
	resp, err := http.Get(info.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Download antwortete mit HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	hash := sha256.New()
	dst := io.MultiWriter(out, hash)

	total := resp.ContentLength
	buf := make([]byte, 256*1024)
	var done int64
	lastReport := time.Time{}
	for {
		if canceled() {
			return errCanceled
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if time.Since(lastReport) > 100*time.Millisecond {
				progress(done, total)
				lastReport = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	progress(done, total)

	if info.SHA256Hash != "" {
		got := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(got, info.SHA256Hash) {
			return fmt.Errorf("Prüfsummenfehler: erwartet %s, erhalten %s", info.SHA256Hash, got)
		}
	}
	return nil
}

// extractZip entpackt die Code-ZIP nach destDir, meldet den Fortschritt
// über progress(done, total) je entpackter Datei und bricht ab, sobald
// canceled() true liefert. Die Portable-ZIP enthält Code.exe direkt auf
// oberster Ebene.
func extractZip(zipPath, destDir string, canceled func() bool, progress func(done, total int)) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	total := len(r.File)
	for i, f := range r.File {
		if canceled() {
			return errCanceled
		}
		target := filepath.Join(destDir, filepath.FromSlash(f.Name))
		// Schutz vor Zip-Slip (Pfade wie "..\..\foo").
		rel, err := filepath.Rel(destDir, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unzulässiger Pfad im Archiv: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		if i%50 == 0 || i == total-1 {
			progress(i+1, total)
		}
	}
	return nil
}

// copyDir kopiert einen Ordner rekursiv (für die Übernahme der
// Benutzerdaten in die neue Version).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	return nil
}
