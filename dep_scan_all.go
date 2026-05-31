package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// depScanMaxDepth bounds how deep discoverRepos walks under each source root.
// Matches the dep-scan script's own default MaxDepth so the two stay in step.
const depScanMaxDepth = 4

// depScanPruneDirs are directory names discoverRepos never descends into. .git
// is skipped for obvious reasons; the rest are heavy dependency caches that
// can't themselves be repos worth listing.
var depScanPruneDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
}

// depScanAllResult bundles everything needed to (re-)render a multi-repo sweep
// report, so the header badge can reopen the last report without rescanning.
type depScanAllResult struct {
	repoLabel  string // e.g. "3 repos" — shown where single-repo uses the repo path
	scriptPath string
	elapsed    time.Duration
	report     string
	count      int
	when       time.Time
}

// depScanCommandMulti is the multi-path sibling of depScanCommand
// ([dep_scan_runner.go]). It resolves the user-level dep-scan script and
// appends every repo path as a trailing argument; the script aggregates all of
// them into one markdown report.
func depScanCommandMulti(paths []string) (*exec.Cmd, string, error) {
	scriptName := "dep-scan.sh"
	if runtime.GOOS == "windows" {
		scriptName = "dep-scan.ps1"
	}

	var scriptPath string
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".claude", "skills", "dep-scan", scriptName)
		if _, err := os.Stat(candidate); err == nil {
			scriptPath = candidate
		}
	}
	if scriptPath == "" {
		home, _ := os.UserHomeDir()
		return nil, "", fmt.Errorf(
			"dep-scan script not found.\n\nLooked for %s in:\n  • %s\n\nInstall the dep-scan skill at ~/.claude/skills/dep-scan/. See README_DEPSCAN.md for install instructions.",
			scriptName, filepath.Join(home, ".claude", "skills", "dep-scan", scriptName),
		)
	}

	if runtime.GOOS == "windows" {
		args := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}, paths...)
		return exec.Command("pwsh", args...), scriptPath, nil
	}
	args := append([]string{scriptPath}, paths...)
	return exec.Command("bash", args...), scriptPath, nil
}

// discoverRepos walks each root (depth-bounded) and returns the absolute,
// de-duplicated, sorted set of directories that look like git repositories
// (contain a .git entry — directory for normal repos, file for submodules/
// worktrees). Once a repo is found we don't descend into it, so nested
// submodules aren't listed as separate sweep targets. Pure function — no
// preferences, no UI — so it's straightforward to unit-test.
func discoverRepos(roots []string, maxDepth int) []string {
	found := make(map[string]bool)

	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rootDepth := strings.Count(filepath.Clean(absRoot), string(os.PathSeparator))

		_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || !d.IsDir() {
				return nil //nolint:nilerr // skip unreadable entries, keep walking
			}
			if depScanPruneDirs[d.Name()] && path != absRoot {
				return filepath.SkipDir
			}
			// Depth bound, relative to the root.
			if strings.Count(filepath.Clean(path), string(os.PathSeparator))-rootDepth > maxDepth {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr == nil {
				found[filepath.Clean(path)] = true
				return filepath.SkipDir // don't descend into a discovered repo
			}
			return nil
		})
	}

	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// effectiveScanRepos is the set "Scan All Repos" actually sweeps: every repo
// discovered under the configured roots, minus the user's opt-out list.
func effectiveScanRepos(a fyne.App) []string {
	discovered := discoverRepos(loadDepScanRoots(a), depScanMaxDepth)
	optOut := make(map[string]bool)
	for _, p := range loadDepScanOptOut(a) {
		optOut[filepath.Clean(p)] = true
	}
	out := discovered[:0:0]
	for _, p := range discovered {
		if !optOut[p] {
			out = append(out, p)
		}
	}
	return out
}

var depScanTotalRe = regexp.MustCompile(`(?m)Total findings \(after severity filter\):\s*\*\*(\d+)\*\*`)

// parseDepScanVulnCount extracts the total-findings number the dep-scan report
// prints in its Summary section. Returns -1 when the line isn't present (e.g.
// the script errored before producing a summary), so the badge can distinguish
// "clean" (0) from "unknown" (-1).
func parseDepScanVulnCount(report string) int {
	m := depScanTotalRe.FindStringSubmatch(report)
	if m == nil {
		return -1
	}
	n := 0
	for _, r := range m[1] {
		n = n*10 + int(r-'0')
	}
	return n
}

// runDepScanAll runs a single dep-scan invocation across every repo in
// effectiveScanRepos and shows the aggregated report. On a clean run it parses
// the total-findings count and hands a fully-populated result to onResult (so
// the caller can update + persist the header badge and stash the report for
// later re-display). Mirrors runDepScanForRepo's progress/goroutine shape.
func runDepScanAll(a fyne.App, parent fyne.Window, onResult func(depScanAllResult)) {
	repos := effectiveScanRepos(a)
	if len(repos) == 0 {
		dialog.ShowConfirm("Scan All Repos",
			"No repositories are configured for scanning yet.\n\nAdd one or more source folders and dep-scan will discover the git repos under them.\n\nOpen the configuration now?",
			func(yes bool) {
				if yes {
					showDepScanConfig(a, parent)
				}
			}, parent)
		return
	}

	cmd, scriptPath, err := depScanCommandMulti(repos)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}

	repoLabel := fmt.Sprintf("%d repos", len(repos))
	progBar := widget.NewProgressBarInfinite()
	progLbl := widget.NewLabel(fmt.Sprintf("Running dep-scan across %d repositories:\n\n%s\n\nUsing script: %s\n\nThis sweeps every repo in one pass and can take a minute or two depending on how many manifests each repo has.", len(repos), bulletList(repos), scriptPath))
	progLbl.Wrapping = fyne.TextWrapWord
	progScroll := container.NewVScroll(progLbl)
	progScroll.SetMinSize(fyne.NewSize(560, 180))
	progContent := container.NewBorder(nil, progBar, nil, nil, progScroll)
	progDlg := dialog.NewCustom("Scan All Repos", "Hide (continues in background)", progContent, parent)
	progDlg.Resize(fyne.NewSize(620, 320))
	progDlg.Show()

	started := time.Now()
	go func() {
		out, runErr := cmd.CombinedOutput()
		elapsed := time.Since(started).Round(time.Millisecond)
		report := string(out)
		fyne.Do(func() {
			progDlg.Hide()
			// Show the report on the next event-loop tick (a second fyne.Do
			// enqueues rather than runs inline), so its scroll lays out
			// against a clean canvas after the progress dialog has fully torn
			// down — belt-and-braces alongside the MultiLineEntry renderer.
			fyne.Do(func() {
				showDepScanReportDialog(a, parent, repoLabel, scriptPath, elapsed, report, runErr)
				// Only update the badge when we got a parseable summary; a hard
				// failure (missing tool) shouldn't claim "clean".
				if count := parseDepScanVulnCount(report); count >= 0 && onResult != nil {
					onResult(depScanAllResult{
						repoLabel:  repoLabel,
						scriptPath: scriptPath,
						elapsed:    elapsed,
						report:     report,
						count:      count,
						when:       time.Now(),
					})
				}
			})
		})
	}()
}

func bulletList(items []string) string {
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  • ")
		b.WriteString(it)
	}
	return b.String()
}

// showDepScanConfig manages the "Scan All Repos" target set: the source-root
// folders to discover under, and per-repo opt-out toggles. Source roots are
// seeded (on first open, when none are configured) from the parents of the
// current recent folders, so the discovered list isn't empty out of the box.
func showDepScanConfig(a fyne.App, parent fyne.Window) {
	roots := loadDepScanRoots(a)
	if len(roots) == 0 {
		roots = seedRootsFromRecents(a)
	}
	// Opt-out set kept as a map for the session; written back on Save.
	optOut := make(map[string]bool)
	for _, p := range loadDepScanOptOut(a) {
		optOut[filepath.Clean(p)] = true
	}

	// --- source roots list ---
	rootsBox := container.NewVBox()
	discoveredBox := container.NewVBox()

	var rebuildRoots func()
	var rebuildDiscovered func()

	rebuildDiscovered = func() {
		discoveredBox.RemoveAll()
		repos := discoverRepos(roots, depScanMaxDepth)
		if len(repos) == 0 {
			discoveredBox.Add(widget.NewLabel("No git repositories found under the configured folders."))
		}
		for _, repo := range repos {
			repo := repo
			chk := widget.NewCheck(repo, func(skip bool) { optOut[repo] = skip })
			chk.SetChecked(optOut[repo])
			discoveredBox.Add(chk)
		}
		discoveredBox.Refresh()
	}

	rebuildRoots = func() {
		rootsBox.RemoveAll()
		if len(roots) == 0 {
			rootsBox.Add(widget.NewLabel("No source folders added yet."))
		}
		for i, r := range roots {
			i, r := i, r
			lbl := widget.NewLabel(r)
			lbl.Truncation = fyne.TextTruncateEllipsis
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				roots = append(roots[:i], roots[i+1:]...)
				rebuildRoots()
				rebuildDiscovered()
			})
			del.Importance = widget.LowImportance
			rootsBox.Add(container.NewBorder(nil, nil, nil, del, lbl))
		}
		rootsBox.Refresh()
	}

	addBtn := widget.NewButtonWithIcon("Add folder…", theme.FolderOpenIcon(), func() {
		d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			p := filepath.Clean(uri.Path())
			for _, existing := range roots {
				if filepath.Clean(existing) == p {
					return // already present
				}
			}
			roots = append(roots, p)
			rebuildRoots()
			rebuildDiscovered()
		}, parent)
		d.Show()
	})

	rebuildRoots()
	rebuildDiscovered()

	intro := widget.NewLabel("“Scan All Repos” discovers every git repository under these folders (searched a few levels deep) and scans them in one pass. Tick a repo to skip it.")
	intro.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(
		intro,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Source folders", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		rootsBox,
		addBtn,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Discovered repositories (tick to skip)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		discoveredBox,
	)
	scroll := container.NewVScroll(body)
	scroll.SetMinSize(fyne.NewSize(560, 380))

	d := dialog.NewCustomConfirm("Configure Repos to Scan", "Save", "Cancel", scroll, func(save bool) {
		if !save {
			return
		}
		saveDepScanRoots(a, roots)
		// Persist only opt-outs that still correspond to discovered repos, so
		// the list doesn't accumulate stale paths.
		current := discoverRepos(roots, depScanMaxDepth)
		var optList []string
		for _, repo := range current {
			if optOut[repo] {
				optList = append(optList, repo)
			}
		}
		saveDepScanOptOut(a, optList)
	}, parent)
	d.Resize(fyne.NewSize(640, 560))
	d.Show()
}

// seedRootsFromRecents proposes initial source roots from the parent folders of
// the explorer's recent-folder list, de-duplicated. Best-effort: an empty
// result is fine — the user just adds folders manually.
func seedRootsFromRecents(a fyne.App) []string {
	seen := make(map[string]bool)
	var out []string
	for _, f := range loadRecentFolders(a) {
		parent := filepath.Dir(strings.TrimSpace(f))
		if parent == "" || parent == "." || seen[parent] {
			continue
		}
		seen[parent] = true
		out = append(out, parent)
	}
	return out
}
