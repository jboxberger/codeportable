//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.128.0", "1.120.0", true},
		{"1.120.0", "1.128.0", false},
		{"1.120.0", "1.120.0", false},
		{"1.9.0", "1.10.0", false}, // numerisch, nicht lexikografisch
		{"1.10.0", "1.9.0", true},
		{"2.0.0", "1.128.5", true},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestPruneOldVersionsKeepsNewest(t *testing.T) {
	oldDir := t.TempDir()
	versions := []string{"1.118.0", "1.119.0", "1.120.0", "1.121.0", "1.123.0", "1.128.0"}
	for _, v := range versions {
		if err := os.MkdirAll(filepath.Join(oldDir, v, "data"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	pruneOldVersions(oldDir, 3)

	entries, err := os.ReadDir(oldDir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	want := []string{"1.121.0", "1.123.0", "1.128.0"}
	if len(got) != len(want) {
		t.Fatalf("nach dem Aufräumen %d Ordner, erwartet %d: %v", len(got), len(want), got)
	}
	for _, v := range want {
		if !got[v] {
			t.Errorf("Version %s hätte behalten werden müssen", v)
		}
	}
}

func TestPruneOldVersionsKeepZeroAndAll(t *testing.T) {
	for _, tc := range []struct{ keep, want int }{{0, 0}, {5, 2}} {
		oldDir := t.TempDir()
		for _, v := range []string{"1.120.0", "1.121.0"} {
			if err := os.MkdirAll(filepath.Join(oldDir, v), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		pruneOldVersions(oldDir, tc.keep)
		entries, err := os.ReadDir(oldDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != tc.want {
			t.Errorf("keep=%d: %d Ordner übrig, erwartet %d", tc.keep, len(entries), tc.want)
		}
	}
}

func TestArchivePathAvoidsCollision(t *testing.T) {
	oldDir := t.TempDir()

	first := archivePath(oldDir, "1.120.0")
	if filepath.Base(first) != "1.120.0" {
		t.Fatalf("erster Archivname = %q, erwartet 1.120.0", filepath.Base(first))
	}
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}

	second := archivePath(oldDir, "1.120.0")
	if filepath.Base(second) != "1.120.0-2" {
		t.Errorf("zweiter Archivname = %q, erwartet 1.120.0-2", filepath.Base(second))
	}

	if base := filepath.Base(archivePath(oldDir, "")); base != "backup" {
		t.Errorf("Archivname ohne Version = %q, erwartet backup", base)
	}
}
