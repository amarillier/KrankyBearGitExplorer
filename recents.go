package main

import (
	"encoding/json"
	"strings"

	"fyne.io/fyne/v2"
)

const (
	prefRecentLeft         = "recentPathsLeft"
	prefRecentRight        = "recentPathsRight"
	prefRecentFolders      = "recentFolderPaths"
	prefShowLineNumbers    = "showLineNumbers"
	prefShowWhitespace     = "showWhitespace"
	prefSyncScroll         = "syncScroll"
	prefSingleDiffWindow   = "singleDiffWindow"
	prefShowDotGit         = "showDotGitInFolderList"
	prefAutoRefresh        = "autoRefreshOnFSChange"
	prefRememberWindowSize = "rememberWindowSize"
	prefWindowWidth        = "windowWidth"
	prefWindowHeight       = "windowHeight"
	prefSplitOffset        = "splitOffset" // main HSplit ratio (0.0–1.0), saved on quit

	// Dep-scan "Scan All Repos" config + last-sweep badge state.
	prefDepScanRoots     = "depScanSourceRoots"   // JSON []string of source-root folders to discover repos under
	prefDepScanOptOut    = "depScanOptOutRepos"   // JSON []string of discovered repo paths to skip
	prefDepScanLastCount = "depScanLastVulnCount" // int: total findings from the last sweep (badge)
	prefDepScanLastTime  = "depScanLastRunUnix"   // int64: Unix seconds of the last sweep (badge tooltip)

	// GitHub Dependabot "scan all" config + last-sweep badge state.
	prefDepAlertOwners          = "depAlertOwners"          // JSON []string of GitHub owners/orgs to enumerate
	prefDepAlertIncludeArchived = "depAlertIncludeArchived" // bool: include archived repos in enumeration
	prefDepAlertLastCount       = "depAlertLastCount"       // int: total open alerts from the last sweep (badge)
	prefDepAlertLastTime        = "depAlertLastRunUnix"     // int64: Unix seconds of the last sweep

	// Daily background Dependabot sweep.
	prefDailyDepAlertEnabled = "dailyDepAlertEnabled" // bool
	prefDailyDepAlertTime    = "dailyDepAlertTime"    // string "HH:MM" (24-hour), default "09:00"
	prefDailyDepAlertLastRun = "dailyDepAlertLastRun" // string "2006-01-02", once-per-day guard

	maxRecentFiles = 10

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

// --- recent folders (explorer) ----------------------------------------------

const maxRecentFolders = 10

func loadRecentFolders(a fyne.App) []string {
	return loadRecentList(a, prefRecentFolders)
}

func addRecentFolder(a fyne.App, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	list := loadRecentList(a, prefRecentFolders)
	out := []string{path}
	for _, p := range list {
		if p == path {
			continue
		}
		out = append(out, p)
		if len(out) >= maxRecentFolders {
			break
		}
	}
	saveRecentList(a, prefRecentFolders, out)
}

func removeRecentFolder(a fyne.App, path string) {
	list := loadRecentList(a, prefRecentFolders)
	if len(list) == 0 {
		return
	}
	out := list[:0]
	for _, p := range list {
		if p != path {
			out = append(out, p)
		}
	}
	saveRecentList(a, prefRecentFolders, out)
}

func clearRecentFolders(a fyne.App) {
	saveRecentList(a, prefRecentFolders, nil)
}

// --- dep-scan "Scan All Repos" config -----------------------------------------
//
// Reuses the generic JSON list load/save helpers above. Roots are the folders
// we walk for git repos; the opt-out list is the set of discovered repos the
// user has chosen to skip in the sweep.

func loadDepScanRoots(a fyne.App) []string  { return loadRecentList(a, prefDepScanRoots) }
func saveDepScanRoots(a fyne.App, p []string) { saveRecentList(a, prefDepScanRoots, p) }

func loadDepScanOptOut(a fyne.App) []string   { return loadRecentList(a, prefDepScanOptOut) }
func saveDepScanOptOut(a fyne.App, p []string) { saveRecentList(a, prefDepScanOptOut, p) }

func loadDepAlertOwners(a fyne.App) []string   { return loadRecentList(a, prefDepAlertOwners) }
func saveDepAlertOwners(a fyne.App, p []string) { saveRecentList(a, prefDepAlertOwners, p) }

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
