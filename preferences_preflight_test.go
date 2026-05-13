package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeFynePrefsFile_emptyRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sanitizeFynePrefsFile(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected empty file removed, stat err=%v", err)
	}
}

func TestSanitizeFynePrefsFile_validUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	const want = `{"theme":"dark"}`
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	sanitizeFynePrefsFile(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSanitizeFynePrefsFile_corruptRenamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	sanitizeFynePrefsFile(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected corrupt original removed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Name(), "corrupt") {
		t.Fatalf("expected one quarantine file, got %#v", entries)
	}
}
