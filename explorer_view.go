package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"

	"github.com/go-git/go-git/v5"
)

// explorerView is the directory + repo explorer main window.
type explorerView struct {
	app fyne.App
	win fyne.Window

	currentPath string
	entries     []explorerEntry
	repo        *git.Repository
	repoRoot    string
	repoModel   *repoTreeModel

	// 0 = worktree (directory listing), 1 = tracked (go-git tree view)
	mode int

	pathLabel   *widget.Label
	branchLabel *widget.Label
	statusLabel *widget.Label
	list        *widget.List
	tree        *widget.Tree
	contentArea *fyne.Container

	modeBtn    *ttwidget.Button
	upBtn      *ttwidget.Button
	reloadBtn  *ttwidget.Button
}

type explorerEntry struct {
	name    string
	isDir   bool
	isGit   bool
	size    int64
	modTime time.Time
	rel     string // repo-relative slash path (empty if not in a repo)
	status  string
}

// runExplorerApp is the app's main entry: boots the Fyne app and shows the
// explorer as the master window.
func runExplorerApp() {
	sanitizeFynePreferencesBeforeLoad(appID)
	a := app.NewWithID(appID)
	loadTheme(a)

	openExplorerWindow(a, true)
	go maybeCheckUpdatesOnLaunch(a)
	a.Run()
}

func openExplorerWindow(a fyne.App, master bool) *explorerView {
	v := &explorerView{app: a}
	w := a.NewWindow(appName)
	v.win = w
	w.SetIcon(resourceKrankyBearNerdPng)
	w.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(v.buildUI()), w.Canvas()))
	v.registerShortcuts(w.Canvas())
	w.Resize(mainWindowLaunchSize(a))
	w.SetOnDropped(v.dropTarget)

	if master {
		w.SetMaster()
		w.SetCloseIntercept(func() {
			fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
			v.quitApp()
		})
	} else {
		w.SetCloseIntercept(func() {
			fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
			windowHide(w)
		})
	}

	v.setupMenus(master)
	windowShow(w)
	return v
}

func (v *explorerView) quitApp() {
	saveMainWindowGeometryIfEnabled(v.app, v.win)
	hideAuxiliaryWindows()
	v.app.Quit()
}

func (v *explorerView) buildUI() fyne.CanvasObject {
	pad := layout.NewCustomPaddedLayout(3, 0, 3, 0)
	titleLbl := widget.NewLabel(appName)
	titleLbl.TextStyle = fyne.TextStyle{Bold: true}
	verLbl := widget.NewLabel("v" + appVersion)
	headerLeft := container.NewHBox(
		container.New(pad, newBrandingHeaderImage(resourceKrankyBearNerdPng)),
		container.NewVBox(titleLbl, verLbl),
	)

	v.pathLabel = widget.NewLabel("(no folder selected — drop a project folder here or click Open Folder…)")
	v.pathLabel.TextStyle = fyne.TextStyle{Bold: true}
	v.pathLabel.Truncation = fyne.TextTruncateEllipsis
	v.branchLabel = widget.NewLabel("")
	v.statusLabel = widget.NewLabel("")
	headerRight := container.NewVBox(v.pathLabel, container.NewHBox(v.branchLabel, v.statusLabel))

	headerRow := container.NewBorder(nil, nil, headerLeft, nil, headerRight)

	browseBtn := ttwidget.NewButtonWithIcon("Open Folder…", theme.FolderOpenIcon(), func() { v.browseFolder() })
	browseBtn.SetToolTip("Choose a project folder to inspect (Cmd/Ctrl+O)")
	browseBtn.Importance = widget.MediumImportance

	v.upBtn = ttwidget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { v.goUp() })
	v.upBtn.SetToolTip("Open the parent folder")
	v.upBtn.Importance = widget.LowImportance
	v.upBtn.Disable()

	v.reloadBtn = ttwidget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { v.refresh() })
	v.reloadBtn.SetToolTip("Reload the current folder and re-run git status (Cmd/Ctrl+R)")
	v.reloadBtn.Importance = widget.LowImportance
	v.reloadBtn.Disable()

	v.modeBtn = ttwidget.NewButtonWithIcon("Tracked files", theme.StorageIcon(), func() { v.toggleMode() })
	v.modeBtn.SetToolTip("Switch to a tree of files tracked by this repo (also reachable by clicking the .git row)")
	v.modeBtn.Importance = widget.LowImportance
	v.modeBtn.Disable()

	toolRow := container.NewHBox(browseBtn, v.upBtn, v.reloadBtn, widget.NewSeparator(), v.modeBtn)

	headerLabels := buildExplorerHeaderRow()
	v.list = v.buildList()
	v.tree = v.buildTree()

	v.contentArea = container.NewMax()
	listWithHeader := container.NewBorder(headerLabels, nil, nil, nil, v.list)
	v.contentArea.Add(listWithHeader)

	topBar := container.NewVBox(headerRow, toolRow, widget.NewSeparator())
	return container.NewBorder(topBar, nil, nil, nil, v.contentArea)
}

// Column widths used for both the header row and each list row, so labels and
// data line up vertically without a real Table widget.
const (
	exploColNameWidth = float32(380)
	exploColSizeWidth = float32(100)
	exploColModWidth  = float32(150)
	exploColStatWidth = float32(220)
)

func sizingRect(w float32) *canvas.Rectangle {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, 1))
	return r
}

const statusHeaderTooltip = `Git status for files in this folder.
• tracked — no changes since last commit
• modified — local edits, not yet staged
• modified (staged) — edited and staged for next commit
• added (staged) — new file added to the index
• untracked — not tracked by git
• ignored — excluded by .gitignore
• deleted — removed from worktree, still in index
• deleted (staged) — deletion staged for next commit
• renamed (staged) — rename staged for next commit
• conflict — merge conflict needs resolution

Full mapping: View → Git Status Legend…`

func buildExplorerHeaderRow() fyne.CanvasObject {
	mk := func(text string, w float32, trailing bool) fyne.CanvasObject {
		lbl := widget.NewLabel(text)
		lbl.TextStyle = fyne.TextStyle{Bold: true}
		if trailing {
			lbl.Alignment = fyne.TextAlignTrailing
		}
		return container.NewMax(sizingRect(w), lbl)
	}
	statusHdr := ttwidget.NewLabelWithStyle("Status", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	statusHdr.SetToolTip(statusHeaderTooltip)
	statusBox := container.NewMax(sizingRect(exploColStatWidth), statusHdr)
	return container.NewVBox(
		container.NewHBox(
			mk("Name", exploColNameWidth, false),
			mk("Size", exploColSizeWidth, true),
			mk("Modified", exploColModWidth, false),
			statusBox,
		),
		widget.NewSeparator(),
	)
}

func (v *explorerView) buildList() *widget.List {
	lst := widget.NewList(
		func() int { return len(v.entries) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			name.Wrapping = fyne.TextWrapOff
			sz := widget.NewLabel("")
			sz.Alignment = fyne.TextAlignTrailing
			mod := widget.NewLabel("")
			st := widget.NewLabel("")
			st.Truncation = fyne.TextTruncateEllipsis
			return container.NewHBox(
				container.NewMax(sizingRect(exploColNameWidth), name),
				container.NewMax(sizingRect(exploColSizeWidth), sz),
				container.NewMax(sizingRect(exploColModWidth), mod),
				container.NewMax(sizingRect(exploColStatWidth), st),
			)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(v.entries) {
				return
			}
			e := v.entries[id]
			row := o.(*fyne.Container)
			name := row.Objects[0].(*fyne.Container).Objects[1].(*widget.Label)
			sz := row.Objects[1].(*fyne.Container).Objects[1].(*widget.Label)
			mod := row.Objects[2].(*fyne.Container).Objects[1].(*widget.Label)
			st := row.Objects[3].(*fyne.Container).Objects[1].(*widget.Label)

			label := e.name
			if e.isDir {
				label += "/"
			}
			name.SetText(label)
			if e.isDir {
				sz.SetText("<DIR>")
			} else {
				sz.SetText(humanSize(e.size))
			}
			mod.SetText(e.modTime.Format("2006-01-02 15:04"))
			st.SetText(humanStatusLabel(e.status))
		},
	)
	lst.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(v.entries) {
			return
		}
		e := v.entries[id]
		if e.isGit {
			v.setMode(1)
			lst.UnselectAll()
			return
		}
		if e.isDir {
			v.loadFolder(filepath.Join(v.currentPath, e.name))
			lst.UnselectAll()
			return
		}
	}
	return lst
}

func (v *explorerView) buildTree() *widget.Tree {
	t := widget.NewTree(
		func(uid widget.TreeNodeID) []widget.TreeNodeID {
			if v.repoModel == nil {
				return nil
			}
			ids := v.repoModel.childIDs(string(uid))
			out := make([]widget.TreeNodeID, 0, len(ids))
			for _, id := range ids {
				out = append(out, widget.TreeNodeID(id))
			}
			return out
		},
		func(uid widget.TreeNodeID) bool {
			if v.repoModel == nil {
				return false
			}
			if string(uid) == "" {
				return true
			}
			return v.repoModel.isBranch(string(uid))
		},
		func(branch bool) fyne.CanvasObject {
			lbl := widget.NewLabel("")
			lbl.Truncation = fyne.TextTruncateEllipsis
			return lbl
		},
		func(uid widget.TreeNodeID, branch bool, o fyne.CanvasObject) {
			lbl := o.(*widget.Label)
			id := string(uid)
			name := id
			if idx := strings.LastIndex(id, "/"); idx >= 0 {
				name = id[idx+1:]
			}
			if branch {
				lbl.SetText(name + "/")
				return
			}
			st := ""
			if v.repoModel != nil {
				st = humanStatusLabel(v.repoModel.fileStatus[id])
			}
			if st != "" && st != "tracked" {
				lbl.SetText(fmt.Sprintf("%s   [%s]", name, st))
			} else {
				lbl.SetText(name)
			}
		},
	)
	return t
}

func (v *explorerView) setMode(mode int) {
	v.mode = mode
	if v.contentArea == nil {
		return
	}
	v.contentArea.Objects = nil
	switch mode {
	case 0:
		v.contentArea.Add(container.NewBorder(buildExplorerHeaderRow(), nil, nil, nil, v.list))
		if v.modeBtn != nil {
			v.modeBtn.SetText("Tracked files")
		}
	case 1:
		v.contentArea.Add(v.tree)
		if v.modeBtn != nil {
			v.modeBtn.SetText("Back to folder")
		}
		v.tree.Refresh()
	}
	v.updateUpButton()
	v.contentArea.Refresh()
}

func (v *explorerView) toggleMode() {
	if v.mode == 0 {
		if v.repoModel == nil {
			dialog.ShowInformation("Not a git repository",
				"Open a folder that is inside a git repository to view tracked files.", v.win)
			return
		}
		v.setMode(1)
		return
	}
	v.setMode(0)
}

func (v *explorerView) browseFolder() {
	d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, v.win)
			return
		}
		if uri == nil {
			return
		}
		v.loadFolder(uri.Path())
	}, v.win)
	d.Show()
}

func (v *explorerView) dropTarget(_ fyne.Position, uris []fyne.URI) {
	if len(uris) == 0 {
		return
	}
	p := uris[0].Path()
	info, err := os.Stat(p)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	if !info.IsDir() {
		p = filepath.Dir(p)
	}
	v.loadFolder(p)
}

func (v *explorerView) loadFolder(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	v.currentPath = abs

	v.repoModel = nil
	v.repo = nil
	v.repoRoot = ""
	if m, repo, err := buildRepoTreeModel(abs); err == nil {
		v.repoModel = m
		v.repo = repo
		v.repoRoot = findRepoRoot(abs)
	}

	if v.mode == 1 && v.repoModel == nil {
		v.setMode(0)
	}
	v.refresh()
}

// findRepoRoot walks up from start until it finds a directory containing a
// `.git` entry. Returns the absolute path of that directory, or "" if none.
func findRepoRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (v *explorerView) refresh() {
	if v.currentPath == "" {
		return
	}
	if v.repoRoot != "" {
		if m, repo, err := buildRepoTreeModel(v.repoRoot); err == nil {
			v.repoModel = m
			v.repo = repo
		}
	}

	entries, err := readDirEntries(v.currentPath, v.repoRoot, v.repoModel)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	v.entries = entries

	v.pathLabel.SetText(v.currentPath)

	v.updateUpButton()
	v.reloadBtn.Enable()

	if v.repo != nil {
		branch := shortBranchName(v.repo)
		v.branchLabel.SetText("Branch: " + branch)
		if wt, err := v.repo.Worktree(); err == nil {
			if st, err := wt.StatusWithOptions(git.StatusOptions{Strategy: git.Preload}); err == nil {
				clean, dirty := countInterestingFiles(st)
				v.statusLabel.SetText(fmt.Sprintf("  •  clean: %d, dirty/untracked: %d", clean, dirty))
			} else {
				v.statusLabel.SetText("")
			}
		}
		v.modeBtn.Enable()
	} else {
		v.branchLabel.SetText("(not a git repository)")
		v.statusLabel.SetText("")
		v.modeBtn.Disable()
	}

	if v.list != nil {
		v.list.Refresh()
	}
	if v.tree != nil {
		v.tree.Refresh()
	}
}

func (v *explorerView) goUp() {
	if v.currentPath == "" {
		return
	}
	// From the tracked-files view, "Up" pops back to the worktree of the
	// same folder (one logical level), matching the user's mental model.
	// Without this, Up would walk to the filesystem parent and step out of
	// the repo entirely on a single click.
	if v.mode == 1 {
		v.setMode(0)
		return
	}
	parent := filepath.Dir(v.currentPath)
	if parent == v.currentPath || parent == "" {
		return
	}
	v.loadFolder(parent)
}

// updateUpButton enables/disables the Up button and refreshes its tooltip to
// reflect the current mode. In tracked mode Up is always available (it returns
// to the folder view); in folder mode it's enabled only when there's a parent.
func (v *explorerView) updateUpButton() {
	if v.upBtn == nil {
		return
	}
	if v.mode == 1 {
		v.upBtn.Enable()
		v.upBtn.SetToolTip("Back to folder view")
		return
	}
	v.upBtn.SetToolTip("Open the parent folder")
	parent := filepath.Dir(v.currentPath)
	if v.currentPath != "" && parent != "" && parent != v.currentPath {
		v.upBtn.Enable()
	} else {
		v.upBtn.Disable()
	}
}

func readDirEntries(path, repoRoot string, m *repoTreeModel) ([]explorerEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]explorerEntry, 0, len(entries))
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			continue
		}
		e := explorerEntry{
			name:    de.Name(),
			isDir:   de.IsDir(),
			isGit:   de.IsDir() && de.Name() == ".git",
			size:    info.Size(),
			modTime: info.ModTime(),
		}
		if repoRoot != "" {
			full := filepath.Join(path, de.Name())
			if rel, err := filepath.Rel(repoRoot, full); err == nil {
				e.rel = filepath.ToSlash(rel)
				if m != nil && !e.isDir {
					if st, ok := m.fileStatus[e.rel]; ok {
						e.status = st
					}
				}
			}
		}
		if e.isGit {
			e.status = "← click to view tracked files"
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.isGit != b.isGit {
			return a.isGit
		}
		if a.isDir != b.isDir {
			return a.isDir
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	return out, nil
}

// humanStatusLabel turns the raw two-character git status code produced by
// formatStatus (in repotree.go) into a friendly description. The raw form is
// "<staging><worktree>" using go-git's StatusCode runes — the same convention
// as `git status --short`. Unknown inputs are returned unchanged so callers
// can safely pass non-status strings (e.g. the .git row's hint text).
func humanStatusLabel(raw string) string {
	if raw == "" || raw == "tracked" {
		return raw
	}
	switch raw {
	case "??":
		return "untracked"
	case "!!":
		return "ignored"
	case "UU", "AA", "DD":
		return "conflict"
	case " M":
		return "modified"
	case "M ":
		return "modified (staged)"
	case "MM":
		return "modified (staged + new edits)"
	case "A ":
		return "added (staged)"
	case "AM":
		return "added (staged + new edits)"
	case " D":
		return "deleted"
	case "D ":
		return "deleted (staged)"
	case "R ":
		return "renamed (staged)"
	case "RM":
		return "renamed (staged + new edits)"
	case "C ":
		return "copied (staged)"
	case " T":
		return "type changed"
	case "T ":
		return "type changed (staged)"
	}
	return raw
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	if exp >= len("KMGTPE") {
		exp = len("KMGTPE") - 1
	}
	suffix := "KMGTPE"[exp : exp+1]
	return fmt.Sprintf("%.1f %sB", float64(n)/float64(div), suffix)
}

func (v *explorerView) registerShortcuts(c fyne.Canvas) {
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyO, Modifier: fyne.KeyModifierShortcutDefault}, func(fyne.Shortcut) {
		v.browseFolder()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierShortcutDefault}, func(fyne.Shortcut) {
		v.refresh()
	})
}

func (v *explorerView) setupMenus(setTray bool) {
	fyne.Do(func() {
		if v.win == nil {
			return
		}
		v.win.SetMainMenu(v.buildMainMenu())
		if setTray {
			if desk, ok := v.app.(desktop.App); ok {
				desk.SetSystemTrayMenu(v.buildTrayMenu())
				desk.SetSystemTrayIcon(resourceKrankyBearNerdPng)
			}
		}
	})
}

func (v *explorerView) buildMainMenu() *fyne.MainMenu {
	openFolder := fyne.NewMenuItem("Open Folder…", func() { v.browseFolder() })
	openFolder.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyO, Modifier: fyne.KeyModifierShortcutDefault}
	refresh := fyne.NewMenuItem("Refresh", func() { v.refresh() })
	refresh.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierShortcutDefault}
	compare := fyne.NewMenuItem("Compare Two Files…", func() { openDiffWindow(v.app, false) })

	file := fyne.NewMenu("File",
		openFolder,
		refresh,
		fyne.NewMenuItemSeparator(),
		compare,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { v.quitApp() }),
	)

	toggleView := fyne.NewMenuItem("Toggle Tracked Files / Folder View", func() { v.toggleMode() })
	legend := fyne.NewMenuItem("Git Status Legend…", func() { v.showStatusLegend() })

	view := fyne.NewMenu("View",
		fyne.NewMenuItem("Show All Windows", func() { bringAllAppWindowsToFront(v.app, v.win) }),
		fyne.NewMenuItem("Hide All Windows", func() { hideAllAppWindows(v.app) }),
		fyne.NewMenuItemSeparator(),
		toggleView,
		legend,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Light Theme", func() { setLightTheme(v.app) }),
		fyne.NewMenuItem("Dark Theme", func() { setDarkTheme(v.app) }),
		fyne.NewMenuItem("System Theme", func() { setSystemTheme(v.app) }),
	)

	help := fyne.NewMenu("Help",
		fyne.NewMenuItem("Help", func() { showHelp(v.app) }),
		fyne.NewMenuItem("About", func() { showAbout(v.app) }),
		fyne.NewMenuItem("Check for Updates…", func() { checkForUpdates(v.app) }),
	)

	return fyne.NewMainMenu(file, view, help)
}

func (v *explorerView) showStatusLegend() {
	rows := [][2]string{
		{"tracked", "Tracked by git, no changes since the last commit."},
		{"modified", "Edited locally in the working tree, not yet staged."},
		{"modified (staged)", "Edited and staged for the next commit."},
		{"modified (staged + new edits)", "Staged, then further edited locally."},
		{"added (staged)", "New file added to the index for the next commit."},
		{"added (staged + new edits)", "Added to the index, then further edited locally."},
		{"untracked", "Present in the working tree but not in the index (and not ignored)."},
		{"ignored", "Excluded by .gitignore (or another ignore source)."},
		{"deleted", "Removed from the working tree but still in the index."},
		{"deleted (staged)", "Deletion staged for the next commit."},
		{"renamed (staged)", "Rename staged for the next commit."},
		{"copied (staged)", "Copy staged for the next commit."},
		{"type changed", "File type (symlink/regular) changed in the working tree."},
		{"conflict", "Merge conflict — needs manual resolution."},
	}
	grid := container.NewGridWithColumns(2)
	for _, r := range rows {
		left := widget.NewLabelWithStyle(r[0], fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		right := widget.NewLabel(r[1])
		right.Wrapping = fyne.TextWrapWord
		grid.Add(left)
		grid.Add(right)
	}
	intro := widget.NewLabel("Statuses shown in the Status column. Mapping follows `git status --short` conventions: a two-character code where the first slot is the index/staged change and the second is the working-tree change.")
	intro.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(intro, widget.NewSeparator(), grid)
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(560, 420))
	d := dialog.NewCustom("Git Status Legend", "Close", scroll, v.win)
	d.Resize(fyne.NewSize(600, 480))
	d.Show()
}

func (v *explorerView) buildTrayMenu() *fyne.Menu {
	return fyne.NewMenu(appName,
		fyne.NewMenuItem("Show All Windows", func() { bringAllAppWindowsToFront(v.app, v.win) }),
		fyne.NewMenuItem("Hide All Windows", func() { hideAllAppWindows(v.app) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Open Folder…", func() { v.browseFolder() }),
		fyne.NewMenuItem("Compare Two Files…", func() { openDiffWindow(v.app, false) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Light Theme", func() { setLightTheme(v.app) }),
		fyne.NewMenuItem("Dark Theme", func() { setDarkTheme(v.app) }),
		fyne.NewMenuItem("System Theme", func() { setSystemTheme(v.app) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Help", func() { showHelp(v.app) }),
		fyne.NewMenuItem("About", func() { showAbout(v.app) }),
		fyne.NewMenuItem("Check for Updates…", func() { checkForUpdates(v.app) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { v.quitApp() }),
	)
}
