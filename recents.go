package main

import (
	"encoding/json"
	"strings"

	"fyne.io/fyne/v2"
)

const (
	prefRecentLeft         = "recentPathsLeft"
	prefRecentRight        = "recentPathsRight"
	prefShowLineNumbers    = "showLineNumbers"
	prefShowWhitespace     = "showWhitespace"
	prefSyncScroll         = "syncScroll"
	prefRememberWindowSize = "rememberWindowSize"
	prefWindowWidth        = "windowWidth"
	prefWindowHeight       = "windowHeight"
	prefSplitOffset        = "splitOffset" // main HSplit ratio (0.0–1.0), saved on quit
	maxRecentFiles         = 10

	mainWindowDefaultWidth  = float32(825)
	mainWindowDefaultHeight = float32(600)
)

func recentKey(side int) string {
	if side == 0 {
		return prefRecentLeft
	}
	return prefRecentRight
}

func loadRecentList(a fyne.App, key string) []string {
	raw := a.Preferences().String(key)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func saveRecentList(a fyne.App, key string, paths []string) {
	if len(paths) == 0 {
		a.Preferences().SetString(key, "")
		return
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return
	}
	a.Preferences().SetString(key, string(b))
}

func addRecent(a fyne.App, side int, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	key := recentKey(side)
	list := loadRecentList(a, key)
	out := []string{path}
	for _, p := range list {
		if p == path {
			continue
		}
		out = append(out, p)
		if len(out) >= maxRecentFiles {
			break
		}
	}
	saveRecentList(a, key, out)
}

func removeRecent(a fyne.App, side int, path string) {
	key := recentKey(side)
	list := loadRecentList(a, key)
	if len(list) == 0 {
		return
	}
	out := list[:0]
	for _, p := range list {
		if p != path {
			out = append(out, p)
		}
	}
	saveRecentList(a, key, out)
}

func clearRecent(a fyne.App, side int) {
	saveRecentList(a, recentKey(side), nil)
}

func clearAllRecent(a fyne.App) {
	clearRecent(a, 0)
	clearRecent(a, 1)
}

func recentMenuLabel(path string) string {
	if path == "" {
		return ""
	}
	const max = 72
	if len(path) <= max {
		return path
	}
	return "…" + path[len(path)-(max-1):]
}

// mainWindowLaunchSize returns the initial main window size from preferences or defaults.
func mainWindowLaunchSize(a fyne.App) fyne.Size {
	if !a.Preferences().BoolWithFallback(prefRememberWindowSize, false) {
		return fyne.NewSize(mainWindowDefaultWidth, mainWindowDefaultHeight)
	}
	sw := float32(a.Preferences().FloatWithFallback(prefWindowWidth, float64(mainWindowDefaultWidth)))
	sh := float32(a.Preferences().FloatWithFallback(prefWindowHeight, float64(mainWindowDefaultHeight)))
	const minW, minH float32 = 400, 300
	const maxW, maxH float32 = 8000, 8000
	if sw < minW || sh < minH || sw > maxW || sh > maxH {
		return fyne.NewSize(mainWindowDefaultWidth, mainWindowDefaultHeight)
	}
	return fyne.NewSize(sw, sh)
}

// saveMainWindowGeometryIfEnabled persists the current window size when the user opted in.
func saveMainWindowGeometryIfEnabled(a fyne.App, w fyne.Window) {
	if w == nil || !a.Preferences().BoolWithFallback(prefRememberWindowSize, false) {
		return
	}
	sz := w.Canvas().Size()
	const minW, minH float32 = 400, 300
	if sz.Width < minW || sz.Height < minH {
		return
	}
	a.Preferences().SetFloat(prefWindowWidth, float64(sz.Width))
	a.Preferences().SetFloat(prefWindowHeight, float64(sz.Height))
}

// loadSplitOffset returns the saved main window left/right split ratio, default 0.5.
func loadSplitOffset(a fyne.App) float64 {
	const def = 0.5
	x := a.Preferences().FloatWithFallback(prefSplitOffset, def)
	if x < 0.05 || x > 0.95 {
		return def
	}
	return x
}
