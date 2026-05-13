//go:build !android && !ios && !wasm

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// fynePrefsFilePath matches fyne.io/fyne/v2/app storage for desktop builds
// (preferences_other.go + internal/app config_*).
func fynePrefsFilePath(uniqueID string) string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Preferences", "fyne", uniqueID, "preferences.json")
	case "windows":
		cfg, _ := os.UserConfigDir()
		return filepath.Join(cfg, "fyne", uniqueID, "preferences.json")
	default:
		cfg, _ := os.UserConfigDir()
		return filepath.Join(cfg, "fyne", uniqueID, "preferences.json")
	}
}

// sanitizeFynePreferencesBeforeLoad removes or quarantines a broken on-disk
// preferences file before fyne loads it. An empty file or a truncated write
// (e.g. two processes saving at once) yields JSON decode EOF / syntax errors
// and Fyne logs "Preferences load error". Treating a missing file as empty
// preferences avoids that. Valid JSON is left untouched.
func sanitizeFynePreferencesBeforeLoad(uniqueID string) {
	sanitizeFynePrefsFile(fynePrefsFilePath(uniqueID))
}

// fyneUpdateCheckStatePath returns the path for go-update-checker's cache file
// (alongside Fyne's preferences.json for this app ID).
func fyneUpdateCheckStatePath(uniqueID string) string {
	return filepath.Join(filepath.Dir(fynePrefsFilePath(uniqueID)), updateCheckStateFileName)
}

func sanitizeFynePrefsFile(path string) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		return
	}
	if fi.Size() == 0 {
		_ = os.Remove(path)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var values map[string]any
	if json.Unmarshal(data, &values) == nil {
		return
	}
	corrupt := fmt.Sprintf("%s.corrupt.%d", path, time.Now().UnixNano())
	if err := os.Rename(path, corrupt); err != nil {
		_ = os.Remove(path)
	}
}
