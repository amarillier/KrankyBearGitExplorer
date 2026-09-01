package main

import (
	"context"
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
	"fyne.io/systray"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// explorerView is the directory + repo explorer main window.
type explorerView struct {
	app fyne.App
	win fyne.Window

	currentPath string
	allEntries  []explorerEntry // unfiltered worktree listing, source of truth
	entries     []explorerEntry // currently-rendered subset (post-filter)
	repo        *git.Repository
	repoRoot    string
	repoModel   *repoTreeModel

	// 0 = worktree (directory listing), 1 = tracked (go-git tree view)
	mode int

	pathLabel       *widget.Label
	branchLabel     *widget.Label
	statusLabel     *widget.Label
	syncLabel       *widget.Label
	releaseLabel    *widget.Label
	visibilityLabel *widget.Label
	depScanLabel    *widget.Label
	depAlertLabel   *widget.Label
	lastCommitLabel *widget.Label

	// lastDepScan holds the most recent "Scan All Repos" sweep so the
	// "View last scan report…" menu item can reopen it without rescanning.
	// nil until the first sweep this session (the header badge can still be
	// seeded from preferences across launches without a stored report).
	lastDepScan *depScanAllResult
	list        *widget.List
	tree        *widget.Tree
	contentArea *fyne.Container

	modeBtn    *ttwidget.Button
	upBtn      *ttwidget.Button
	reloadBtn  *ttwidget.Button
	historyBtn *ttwidget.Button
	healthBtn  *ttwidget.Button
	depScanBtn *ttwidget.Button
	viewBtn    *ttwidget.Button
	repoBtn    *ttwidget.Button
	diffBtn    *ttwidget.Button

	// selectedFileRel / selectedFileAbs hold the repo-relative and on-disk
	// paths of the currently-selected *file* row in the folder list. Reset
	// whenever the selection moves off a file (directory click, .git click,
	// folder reload, or an UnselectAll). Read by the View ▾ button's
	// "Blame…" item to decide whether to enable.
	selectedFileRel string
	selectedFileAbs string

	// "What's new since I last looked" banner above the filter bar.
	// whatsNewBaseSHA is the stored marker SHA captured when the banner
	// is shown — used by the Compare button to pre-set the Repo History
	// compare base. See whats_new.go for the full flow.
	whatsNewRow     *fyne.Container
	whatsNewLabel   *widget.Label
	whatsNewBaseSHA plumbing.Hash

	// Filter bar — applies to both folder view and tracked-files tree.
	filter                 explorerFilter
	filterEntry            *widget.Entry
	filterDirtyCheck       *widget.Check
	filterUntrackedCheck   *widget.Check
	filterOnlyIgnoredCheck *widget.Check
	filterIgnoredCheck     *widget.Check
	// suspendFilters short-circuits the filter widgets' OnChanged handlers
	// while we programmatically reset them on folder change, so we don't
	// fan out N redundant applyFilters() calls.
	suspendFilters bool

	// watcher, when non-nil, fires the explorer's refresh after filesystem
	// changes under the current folder or the repo's .git/index. Restarted
	// on every loadFolder; torn down on window close. Driven by the
	// "Auto-refresh when files change outside the app" preference.
	watcher *repoWatcher

	// dailyScan, when non-nil, runs the once-daily background Dependabot
	// sweep at the user-configured time (passive 🛡 badge update). Started on
	// the master window, restarted on prefs change, stopped on window close.
	dailyScan *dailyScanScheduler

	// contextPop is the active row context-menu popup, kept so we can tear
	// down its tooltip layer before opening a new one.
	contextPop *widget.PopUp
}

type explorerEntry struct {
	name    string
	isDir   bool
	isGit   bool
	size    int64
	modTime time.Time
	rel     string // repo-relative slash path (empty if not in a repo)
	status  string

	// rollup is the non-clean child summary for directory entries. Zero
	// value for files and for clean directories. Used by the filter bar
	// to decide whether a directory passes the "Only dirty / untracked"
	// gate — without rollup, dirs would always pass (they have no
	// status of their own), which made those filters noisier than useful.
	rollup rollupCounts
}

// explorerPrefsChangedHook is set when the explorer window opens. Used by the
// shared preferences dialog to ask the explorer to re-read its preferences
// after the user clicks Save (refreshes the menu's Recent Folders submenu
// and the worktree listing when "Show .git" toggled).
var explorerPrefsChangedHook func()

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
			v.advanceLastSeenHeadForCurrentRepo()
			if v.watcher != nil {
				v.watcher.stop()
				v.watcher = nil
			}
			v.stopDailyScanScheduler()
			fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
			v.quitApp()
		})
	} else {
		w.SetCloseIntercept(func() {
			v.advanceLastSeenHeadForCurrentRepo()
			if v.watcher != nil {
				v.watcher.stop()
				v.watcher = nil
			}
			fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
			windowHide(w)
		})
	}

	v.setupMenus(master)
	explorerPrefsChangedHook = func() {
		v.refresh()
		v.refreshMenu()
		// The auto-refresh toggle is one of the prefs the dialog can flip;
		// rebuild the watcher so the new on/off state takes effect without
		// requiring a folder reload.
		v.restartWatcher()
		// Daily-scan time/toggle may have changed — restart the scheduler so
		// it picks up the new schedule without a relaunch.
		v.startDailyScanScheduler()
	}
	windowShow(w)
	if master {
		v.startDailyScanScheduler()
	}
	return v
}

func (v *explorerView) quitApp() {
	v.advanceLastSeenHeadForCurrentRepo()
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
	v.syncLabel = widget.NewLabel("")
	v.releaseLabel = widget.NewLabel("")
	v.visibilityLabel = widget.NewLabel("")
	v.depScanLabel = widget.NewLabel("")
	v.seedDepScanBadge()
	v.depAlertLabel = widget.NewLabel("")
	v.seedDepAlertBadge()
	v.lastCommitLabel = widget.NewLabel("")
	v.lastCommitLabel.TextStyle = fyne.TextStyle{Italic: true}
	v.lastCommitLabel.Truncation = fyne.TextTruncateEllipsis
	headerRight := container.NewVBox(
		v.pathLabel,
		container.NewHBox(v.branchLabel, v.statusLabel, v.syncLabel, v.releaseLabel, v.visibilityLabel, v.depScanLabel, v.depAlertLabel),
		v.lastCommitLabel,
	)

	headerRow := container.NewBorder(nil, nil, headerLeft, nil, headerRight)

	browseBtn := ttwidget.NewButtonWithIcon("Open Folder…", theme.FolderOpenIcon(), func() { v.browseFolder() })
	browseBtn.SetToolTip("Choose a project folder to inspect (Cmd/Ctrl+O)")
	browseBtn.Importance = widget.MediumImportance

	var recentBtn *ttwidget.Button
	recentBtn = ttwidget.NewButtonWithIcon("Recent ▾", theme.FolderIcon(), func() {
		menu := v.buildRecentFoldersSubmenu()
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(recentBtn)
		pos.Y += recentBtn.Size().Height
		widget.ShowPopUpMenuAtPosition(menu, v.win.Canvas(), pos)
	})
	recentBtn.SetToolTip("Open a recently-opened project folder")
	recentBtn.Importance = widget.LowImportance

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

	v.historyBtn = ttwidget.NewButtonWithIcon("History", theme.HistoryIcon(), func() { v.openHistory() })
	v.historyBtn.SetToolTip("Open the repo history window — commits + per-file diff against the parent commit")
	v.historyBtn.Importance = widget.LowImportance
	v.historyBtn.Disable()

	v.healthBtn = ttwidget.NewButtonWithIcon("Health", theme.InfoIcon(), func() { v.openRepoHealth() })
	v.healthBtn.SetToolTip("Open the Repo Health dialog — object-database stats + git fsck verify + copy-paste CLI hints")
	v.healthBtn.Importance = widget.LowImportance
	v.healthBtn.Disable()

	v.depScanBtn = ttwidget.NewButtonWithIcon("Scan ▾", theme.SearchIcon(), func() {
		menu := v.buildScanDropdownMenu()
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(v.depScanBtn)
		pos.Y += v.depScanBtn.Size().Height
		widget.ShowPopUpMenuAtPosition(menu, v.win.Canvas(), pos)
	})
	v.depScanBtn.SetToolTip("Dependency scans — local dep-scan (osv-scanner + govulncheck) or GitHub Dependabot, for this repo or all repos")
	v.depScanBtn.Importance = widget.LowImportance

	v.diffBtn = ttwidget.NewButtonWithIcon("Diff", theme.ContentCopyIcon(), func() { openDiffWindow(v.app, false) })
	v.diffBtn.SetToolTip("Open the two-pane diff window — pick any two files to compare side-by-side")
	v.diffBtn.Importance = widget.LowImportance

	v.viewBtn = ttwidget.NewButtonWithIcon("View ▾", theme.VisibilityIcon(), func() {
		menu := v.buildViewDropdownMenu()
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(v.viewBtn)
		pos.Y += v.viewBtn.Size().Height
		widget.ShowPopUpMenuAtPosition(menu, v.win.Canvas(), pos)
	})
	v.viewBtn.SetToolTip("Open a repo data view (Blame, Branches, Contributors, git log -p, Git Status Legend, Reflog, Remotes, Repo Config, Repo Health, Repo History, Stashes, Tags)")
	v.viewBtn.Importance = widget.LowImportance
	v.viewBtn.Disable()

	// Repo ▾ is the write-ops sibling to View ▾. Stays enabled regardless of
	// repo state — the menu items inside enable/disable themselves based on
	// whether a repo is currently loaded.
	v.repoBtn = ttwidget.NewButtonWithIcon("Repo ▾", theme.FolderIcon(), func() {
		menu := v.buildRepoDropdownMenu()
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(v.repoBtn)
		pos.Y += v.repoBtn.Size().Height
		widget.ShowPopUpMenuAtPosition(menu, v.win.Canvas(), pos)
	})
	v.repoBtn.SetToolTip("Repository management actions (Initialize Repository Here, Local Repo Identity)")
	v.repoBtn.Importance = widget.LowImportance

	toolRow := container.NewHBox(browseBtn, recentBtn, v.upBtn, v.reloadBtn, widget.NewSeparator(), v.modeBtn, v.repoBtn, v.viewBtn, v.historyBtn, v.healthBtn, v.depScanBtn, v.diffBtn)

	v.filterEntry = widget.NewEntry()
	v.filterEntry.SetPlaceHolder("Filter by name…")
	v.filterEntry.OnChanged = func(s string) {
		if v.suspendFilters {
			return
		}
		v.filter.nameContains = strings.ToLower(strings.TrimSpace(s))
		v.applyFilters()
	}
	v.filterDirtyCheck = widget.NewCheck("Only dirty", func(b bool) {
		if v.suspendFilters {
			return
		}
		v.filter.onlyDirty = b
		v.applyFilters()
	})
	v.filterUntrackedCheck = widget.NewCheck("Only untracked", func(b bool) {
		if v.suspendFilters {
			return
		}
		v.filter.onlyUntracked = b
		v.applyFilters()
	})
	v.filterOnlyIgnoredCheck = widget.NewCheck("Only ignored", func(b bool) {
		if v.suspendFilters {
			return
		}
		v.filter.onlyIgnored = b
		v.applyFilters()
	})
	v.filterIgnoredCheck = widget.NewCheck("Show ignored", func(b bool) {
		if v.suspendFilters {
			return
		}
		v.filter.showIgnored = b
		v.applyFilters()
	})
	filterRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(v.filterDirtyCheck, v.filterUntrackedCheck, v.filterOnlyIgnoredCheck, v.filterIgnoredCheck),
		v.filterEntry,
	)

	v.whatsNewRow = v.buildWhatsNewBanner()

	headerLabels := buildExplorerHeaderRow()
	v.list = v.buildList()
	v.tree = v.buildTree()

	v.contentArea = container.NewMax()
	listWithHeader := container.NewBorder(headerLabels, nil, nil, nil, v.list)
	v.contentArea.Add(listWithHeader)

	topBar := container.NewVBox(headerRow, toolRow, v.whatsNewRow, filterRow, widget.NewSeparator())
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
• submodule — separate repo embedded here; click to descend into it
• contains submodule — this directory has a submodule somewhere inside
• deleted — removed from worktree, still in index
• deleted (staged) — deletion staged for next commit
• renamed (staged) — rename staged for next commit
• conflict — merge conflict needs resolution
• modified (N) / untracked (N) / conflict (N) on a directory row —
  rolled-up count of non-clean files anywhere in its subtree

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
			inner := container.NewHBox(
				container.NewMax(sizingRect(exploColNameWidth), name),
				container.NewMax(sizingRect(exploColSizeWidth), sz),
				container.NewMax(sizingRect(exploColModWidth), mod),
				container.NewMax(sizingRect(exploColStatWidth), st),
			)
			// Wrap the row so it can receive secondary tap (right-click) for
			// the per-row context menu, while primary tap still falls through
			// to the list's OnSelected logic.
			return newExplorerRow(v, inner)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(v.entries) {
				return
			}
			e := v.entries[id]
			wrap := o.(*explorerRow)
			wrap.rowID = id
			row := wrap.inner.(*fyne.Container)
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
			v.selectedFileRel = ""
			v.selectedFileAbs = ""
			return
		}
		e := v.entries[id]
		if e.isGit {
			v.selectedFileRel = ""
			v.selectedFileAbs = ""
			v.setMode(1)
			lst.UnselectAll()
			return
		}
		if e.isDir {
			v.selectedFileRel = ""
			v.selectedFileAbs = ""
			target := filepath.Join(v.currentPath, e.name)
			// Submodules are their own repos; promote to recents on entry so
			// the user can hop back without re-navigating from the parent.
			if e.status == "submodule" {
				addRecentFolder(v.app, target)
				v.refreshMenu()
			}
			v.loadFolder(target)
			lst.UnselectAll()
			return
		}
		// File row selected — remember it so View ▾ → Blame… can act on it.
		v.selectedFileRel = e.rel
		v.selectedFileAbs = filepath.Join(v.currentPath, e.name)
	}
	lst.OnUnselected = func(_ widget.ListItemID) {
		v.selectedFileRel = ""
		v.selectedFileAbs = ""
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
				label := name + "/"
				if v.repoModel != nil {
					if rl := v.repoModel.dirRollup[id].label(); rl != "" {
						label = fmt.Sprintf("%s   [%s]", name+"/", rl)
					}
				}
				lbl.SetText(label)
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
		addRecentFolder(v.app, uri.Path())
		v.loadFolder(uri.Path())
		v.refreshMenu()
	}, v.win)
	d.Resize(folderDialogSize(v.win))
	d.Show()
}

// folderDialogSize sizes the folder-picker relative to the main window so
// browsing/selecting directories isn't cramped on larger displays, while
// still guaranteeing a roomy minimum on small windows.
func folderDialogSize(win fyne.Window) fyne.Size {
	const minW, minH float32 = 900, 650
	base := win.Canvas().Size()
	w := base.Width * 0.85
	h := base.Height * 0.85
	if w < minW {
		w = minW
	}
	if h < minH {
		h = minH
	}
	return fyne.NewSize(w, h)
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
	addRecentFolder(v.app, p)
	v.loadFolder(p)
	v.refreshMenu()
}

// openRecentFolder is the click handler for the File → Open Recent Folder
// submenu. Validates the path still exists (drops it from recents if not),
// promotes it to the top of the recents list, loads it, and refreshes the
// menu so the new ordering shows up next time the submenu is opened.
func (v *explorerView) openRecentFolder(path string) {
	if _, err := os.Stat(path); err != nil {
		removeRecentFolder(v.app, path)
		v.refreshMenu()
		dialog.ShowError(fmt.Errorf("recent folder no longer exists: %s", path), v.win)
		return
	}
	addRecentFolder(v.app, path)
	v.loadFolder(path)
	v.refreshMenu()
}

// refreshMenu rebuilds and re-applies the explorer's window menu. Used after
// state changes that the menu reflects (recent folders list mainly).
func (v *explorerView) refreshMenu() {
	fyne.Do(func() {
		if v.win == nil {
			return
		}
		v.win.SetMainMenu(v.buildMainMenu())
	})
}

func (v *explorerView) loadFolder(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}

	// Repo-switch guard: if we're crossing into a different repo (or out of
	// one) while repo-bound child windows are still open, ask the user what
	// to do before any state changes — those windows would otherwise keep
	// showing data from the previous repo, which is a footgun.
	targetRoot := findRepoRoot(abs)
	if targetRoot != v.repoRoot && len(repoChildWindows) > 0 {
		v.promptRepoSwitch(abs, targetRoot, func(proceed, closeWindows bool) {
			if !proceed {
				return
			}
			if closeWindows {
				closeAllRepoChildWindows()
			}
			v.doLoadFolder(abs)
		})
		return
	}
	v.doLoadFolder(abs)
}

func (v *explorerView) doLoadFolder(abs string) {
	// If we're leaving a previously-loaded repo for a different one,
	// advance the prior repo's "last seen HEAD" marker so closing-and-
	// reopening it later doesn't re-show the banner for commits the user
	// already saw on this trip.
	prevRepoRoot := v.repoRoot
	targetRoot := findRepoRoot(abs)
	if prevRepoRoot != "" && prevRepoRoot != targetRoot {
		v.advanceLastSeenHeadForCurrentRepo()
	}

	v.currentPath = abs
	v.selectedFileRel = ""
	v.selectedFileAbs = ""

	// Only reset filters when the repo context changes — intra-repo
	// navigation (into subdirs, back up to the root) preserves the
	// user's filter intent. A filter like "Only dirty" expresses what
	// they want to see in *this* project; walking around inside it
	// shouldn't keep flipping it off. Repo switches still reset, so
	// stale filters from a previous project don't follow you across.
	if prevRepoRoot != targetRoot {
		v.resetFilters()
	}

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
	v.evaluateWhatsNew()
	v.restartWatcher()
}

// restartWatcher tears down any existing fsnotify watcher and (when the
// "Auto-refresh" preference is on) spins up a new one against the current
// folder + the repo's .git/index. Wrapping the refresh in fyne.Do bounces
// off the fsnotify goroutine onto Fyne's UI thread, where list/tree
// widget updates are required to live.
func (v *explorerView) restartWatcher() {
	if v.watcher != nil {
		v.watcher.stop()
		v.watcher = nil
	}
	if !v.app.Preferences().BoolWithFallback(prefAutoRefresh, true) {
		return
	}
	if v.currentPath == "" {
		return
	}
	targets := repoWatchTargets(v.currentPath, v.repoRoot)
	v.watcher = startRepoWatcher(targets, func() {
		fyne.Do(func() {
			if v.currentPath == "" {
				return
			}
			v.refresh()
		})
	})
}

// promptRepoSwitch surfaces a modal dialog listing the still-open repo-bound
// child windows and asking whether to close them, keep them open, or cancel
// the repo switch entirely. Built with dialog.NewCustom (single dismiss =
// "Cancel") plus two action buttons embedded in the content.
func (v *explorerView) promptRepoSwitch(targetAbs, targetRoot string, cb func(proceed, closeWindows bool)) {
	snapshot := append([]fyne.Window(nil), repoChildWindows...)

	from := v.repoRoot
	if from == "" {
		from = "(current folder)"
	}
	to := targetRoot
	if to == "" {
		to = targetAbs + "  (not a git repository)"
	}

	intro := widget.NewLabel(fmt.Sprintf("Switching from %s to %s will leave these windows showing data from the previous repo:", from, to))
	intro.Wrapping = fyne.TextWrapWord

	listBox := container.NewVBox()
	for _, w := range snapshot {
		title := "(untitled window)"
		if w != nil {
			if t := w.Title(); t != "" {
				title = t
			}
		}
		listBox.Add(widget.NewLabel("  • " + title))
	}

	question := widget.NewLabel("Close them, or keep them open?")
	question.TextStyle = fyne.TextStyle{Bold: true}

	var dlg dialog.Dialog
	closeBtn := widget.NewButton("Close them and continue", func() {
		dlg.Hide()
		cb(true, true)
	})
	closeBtn.Importance = widget.HighImportance
	keepBtn := widget.NewButton("Keep them open and continue", func() {
		dlg.Hide()
		cb(true, false)
	})
	actions := container.NewHBox(layout.NewSpacer(), closeBtn, keepBtn)

	content := container.NewVBox(intro, listBox, widget.NewSeparator(), question, actions)
	dlg = dialog.NewCustom("Switching repositories", "Cancel", content, v.win)
	dlg.Resize(fyne.NewSize(560, 320))
	dlg.Show()
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

	showDotGit := v.app.Preferences().BoolWithFallback(prefShowDotGit, false)
	entries, err := readDirEntries(v.currentPath, v.repoRoot, v.repoModel, showDotGit)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	v.allEntries = entries
	v.applyFilters()

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
		v.lastCommitLabel.SetText(latestCommitSummary(v.repo))
		v.refreshRemoteSync()
		v.refreshReleaseStatus()
		v.refreshVisibility()
		v.modeBtn.Enable()
		v.historyBtn.Enable()
		v.healthBtn.Enable()
		v.viewBtn.Enable()
	} else {
		v.branchLabel.SetText("(not a git repository)")
		v.statusLabel.SetText("")
		v.syncLabel.SetText("")
		v.releaseLabel.SetText("")
		v.lastCommitLabel.SetText("")
		v.modeBtn.Disable()
		v.historyBtn.Disable()
		v.healthBtn.Disable()
		v.viewBtn.Disable()
	}

	if v.list != nil {
		v.list.Refresh()
	}
	if v.tree != nil {
		v.tree.Refresh()
	}
}

// applyFilters rebuilds v.entries (folder list) and v.repoModel.visible
// (tracked-files tree) from v.allEntries and the active filter state, then
// refreshes the underlying widgets. Cheap to call — re-runs on every filter
// widget change and on every refresh().
func (v *explorerView) applyFilters() {
	v.entries = filterExplorerEntries(v.allEntries, v.filter)
	if v.repoModel != nil {
		v.repoModel.visible = v.repoModel.computeVisible(v.filter)
	}
	if v.list != nil {
		v.list.Refresh()
	}
	if v.tree != nil {
		v.tree.Refresh()
	}
}

// refreshRemoteSync paints the header's sync indicator from the local
// cached state (instant — three `git` shellouts that read refs, no
// network) and kicks off an asynchronous ls-remote probe to verify the
// remote is actually reachable. The async result lands via fyne.Do and
// only updates the label if the user hasn't navigated to a different
// repo in the meantime.
//
// When no upstream is configured, the indicator says so and we skip the
// live probe — there's nothing to probe against.
func (v *explorerView) refreshRemoteSync() {
	if v.syncLabel == nil {
		return
	}
	if v.repoRoot == "" {
		v.syncLabel.SetText("")
		return
	}
	info := readLocalSync(v.repoRoot)
	v.syncLabel.SetText("  •  " + info.label())
	if !info.HasUpstream {
		return
	}
	snapshotRoot := v.repoRoot
	upstreamRef := info.UpstreamRef
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteCheckTimeout)
		defer cancel()
		ok, err := checkRemoteReachable(ctx, snapshotRoot, upstreamRef)
		fyne.Do(func() {
			// Guard: if the user has switched folders since we started,
			// the label is now showing a different repo's status — don't
			// stomp on it with the stale probe result.
			if v.syncLabel == nil || v.repoRoot != snapshotRoot {
				return
			}
			info.LiveChecked = true
			info.Reachable = ok
			info.LiveErr = err
			v.syncLabel.SetText("  •  " + info.label())
		})
	}()
}

// refreshReleaseStatus paints the header's release indicator: whether
// HEAD is at the latest semver tag (released) or how many commits sit
// between the last tag and HEAD. Two `git` shellouts that read local
// refs only — no network.
func (v *explorerView) refreshReleaseStatus() {
	if v.releaseLabel == nil {
		return
	}
	if v.repoRoot == "" {
		v.releaseLabel.SetText("")
		return
	}
	info := detectReleaseStatus(v.repoRoot)
	v.releaseLabel.SetText("  •  " + info.label())
}

// refreshVisibility paints the header's GitHub visibility indicator
// (public/private/internal). Git has no concept of this — it's an API
// property — so it always requires a gh call; mirrors refreshRemoteSync's
// async-with-stale-guard pattern rather than blocking the UI thread.
func (v *explorerView) refreshVisibility() {
	if v.visibilityLabel == nil {
		return
	}
	if v.repo == nil {
		v.visibilityLabel.SetText("")
		return
	}
	host, owner, name, ok := findGitHubReleaseTarget(v.repo)
	if !ok {
		v.visibilityLabel.SetText("")
		return
	}
	snapshotRoot := v.repoRoot
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), visibilityCheckTimeout)
		defer cancel()
		info, _ := checkRepoVisibility(ctx, host, owner, name)
		fyne.Do(func() {
			if v.visibilityLabel == nil || v.repoRoot != snapshotRoot {
				return
			}
			if label := info.label(); label != "" {
				v.visibilityLabel.SetText("  •  " + label)
			} else {
				v.visibilityLabel.SetText("")
			}
		})
	}()
}

// resetFilters clears the filter state and the widgets back to defaults.
// Called on repo switch (not on intra-repo navigation) so each project
// starts from a clean slate — no stale "I forgot the filter was on"
// moments after switching repos, while filter intent ("Only dirty") is
// preserved as you browse around inside a single repo.
func (v *explorerView) resetFilters() {
	v.filter = explorerFilter{}
	if v.filterEntry == nil {
		return
	}
	v.suspendFilters = true
	v.filterEntry.SetText("")
	v.filterDirtyCheck.SetChecked(false)
	v.filterUntrackedCheck.SetChecked(false)
	v.filterOnlyIgnoredCheck.SetChecked(false)
	v.filterIgnoredCheck.SetChecked(false)
	v.suspendFilters = false
}

// filterExplorerEntries applies the filter to the folder view's row list.
// The .git pinned row bypasses all filters. Files run through the full status
// filter (leafStatusPasses). Directories are gated on:
//
//   - Show-ignored: an ignored dir is hidden unless "Show ignored" or "Only
//     ignored" is on, matching the rule for ignored files.
//   - "Only dirty / untracked": a dir passes only if its rollup contains
//     children of that category. Submodule dirs are treated as opaque (a
//     separate repo), so they always pass these gates for navigation.
//
// "Only ignored" applied to directories matches only dirs that are themselves
// ignored — we don't roll up ignored content (go-git's Status() omits ignored
// files, so the rollup wouldn't see them anyway).
func filterExplorerEntries(in []explorerEntry, f explorerFilter) []explorerEntry {
	out := make([]explorerEntry, 0, len(in))
	for _, e := range in {
		if !e.isGit {
			if e.isDir {
				human := humanStatusLabel(e.status)
				isIgnored := human == "ignored"
				isSubmodule := human == "submodule"
				if isIgnored && !f.showIgnored && !f.onlyIgnored {
					continue
				}
				if f.onlyDirty || f.onlyUntracked || f.onlyIgnored {
					match := false
					if isSubmodule {
						// Submodules are separate repos; don't claim to know
						// their internals — always pass so the user can
						// drill in.
						match = true
					} else {
						if f.onlyDirty && (e.rollup.dirty+e.rollup.conflict) > 0 {
							match = true
						}
						if f.onlyUntracked && e.rollup.untracked > 0 {
							match = true
						}
						if f.onlyIgnored && isIgnored {
							match = true
						}
					}
					if !match {
						continue
					}
				}
			} else if !leafStatusPasses(e.status, f) {
				continue
			}
		}
		if f.nameContains != "" && !strings.Contains(strings.ToLower(e.name), f.nameContains) {
			continue
		}
		out = append(out, e)
	}
	return out
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

func readDirEntries(path, repoRoot string, m *repoTreeModel, showDotGit bool) ([]explorerEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]explorerEntry, 0, len(entries))
	for _, de := range entries {
		// .git is intentionally hidden by default — most users don't need to
		// see it in the worktree list, and the "Tracked files" toolbar button
		// is the proper affordance for inspecting what git is tracking.
		// Preferences → "Show .git in folder listings" surfaces it again.
		if !showDotGit && de.IsDir() && de.Name() == ".git" {
			continue
		}
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
				if m != nil {
					if !e.isDir {
						if st, ok := m.fileStatus[e.rel]; ok {
							e.status = st
						} else if m.isIgnored(e.rel, false) {
							e.status = "!!"
						}
					} else if !e.isGit {
						// Stamp the rollup on every non-special dir so filter
						// logic can use it even when a more-specific label
						// (submodule / ignored) wins for display.
						e.rollup = m.dirRollup[e.rel]
						_, isSubmodule := m.submodules[e.rel]
						_, isSubAncestor := m.submoduleAncestors[e.rel]
						switch {
						case isSubmodule:
							e.status = "submodule"
						case m.isIgnored(e.rel, true):
							e.status = "!!"
						case e.rollup.total() > 0:
							e.status = e.rollup.label()
						case isSubAncestor:
							e.status = "contains submodule"
						}
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

// latestCommitSummary returns a single line describing the HEAD commit for
// display in the explorer's repo header: "Last: <subject> · <author> · <when>".
// Returns "(no commits)" when the repo has no commits yet, "" on any other
// failure so the header simply stays empty.
func latestCommitSummary(repo *git.Repository) string {
	head, err := repo.Head()
	if err != nil {
		return "(no commits)"
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return ""
	}
	return fmt.Sprintf("Last: %s · %s · %s",
		firstLine(commit.Message),
		commit.Author.Name,
		humanRelativeTime(commit.Author.When),
	)
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

				// Hover tooltip on the tray icon (Windows/macOS; no-op on
				// Linux) -- desktop.App has no tooltip setter, but
				// fyne.io/systray (what Fyne's own driver already uses
				// internally for the tray icon) does. Deferred slightly
				// since, unlike Fyne's own SetSystemTrayIcon call above, a
				// raw systray.SetTooltip call has no built-in retry/caching
				// if the tray isn't fully ready yet.
				time.AfterFunc(300*time.Millisecond, func() {
					systray.SetTooltip(appName)
				})
			}
		}
	})
}

func (v *explorerView) buildMainMenu() *fyne.MainMenu {
	openFolder := fyne.NewMenuItem("Open Folder…", func() { v.browseFolder() })
	openFolder.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyO, Modifier: fyne.KeyModifierShortcutDefault}
	openRecent := fyne.NewMenuItem("Open Recent Folder", nil)
	openRecent.ChildMenu = v.buildRecentFoldersSubmenu()
	refresh := fyne.NewMenuItem("Refresh", func() { v.refresh() })
	refresh.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierShortcutDefault}
	compare := fyne.NewMenuItem("Compare Two Files…", func() { openDiffWindow(v.app, false) })
	prefs := fyne.NewMenuItem("Preferences…", func() { showPreferences(v.app, nil) })

	scanDeps := fyne.NewMenuItem("Scan Dependencies…", func() { v.runDepScan() })
	scanAll := fyne.NewMenuItem("Scan All Repos…", func() { v.runDepScanAll() })
	scanAllCfg := fyne.NewMenuItem("Configure Repos to Scan…", func() { showDepScanConfig(v.app, v.win) })
	// Always enabled — showLastDepScanReport prompts a fresh sweep when there's
	// no report yet, which keeps this correct without re-running buildMainMenu
	// after every scan just to flip the disabled flag.
	lastScanReport := fyne.NewMenuItem("View Last Scan Report…", func() { v.showLastDepScanReport() })
	auditRepos := fyne.NewMenuItem("Audit Local Repos…", func() { runRepoAudit(v.app, v.win) })

	depAlertRepo := fyne.NewMenuItem("Dependabot: Check This Repo…", func() { v.checkDependabotAlerts() })
	depAlertRepo.Disabled = v.repo == nil
	depAlertAll := fyne.NewMenuItem("Dependabot: Scan All Repos…", func() { v.runDependabotScanAll() })
	depAlertCfg := fyne.NewMenuItem("Dependabot: Configure Owners…", func() { showDepAlertConfig(v.app, v.win) })

	// Git management entries — disabled state depends on whether the current
	// folder is inside a repo. buildMainMenu is re-run via refreshMenu on
	// folder changes (see loadFolder), so these flags stay accurate as the
	// user navigates.
	commit := fyne.NewMenuItem("Commit…", func() { v.commitChanges() })
	commit.Disabled = v.repo == nil
	initRepo := fyne.NewMenuItem("Initialize Repository Here…", func() { v.initRepoHere() })
	initRepo.Disabled = v.repo != nil || v.currentPath == ""
	repoIdentity := fyne.NewMenuItem("Local Repo Identity…", func() { v.editRepoIdentity() })
	repoIdentity.Disabled = v.repo == nil
	manageRemotes := fyne.NewMenuItem("Manage Remotes…", func() { v.manageRemotes() })
	manageRemotes.Disabled = v.repo == nil
	pull := fyne.NewMenuItem("Pull…", func() { v.pullChanges() })
	pull.Disabled = v.repo == nil
	push := fyne.NewMenuItem("Push…", func() { v.pushChanges() })
	push.Disabled = v.repo == nil
	release := fyne.NewMenuItem("Release…", func() { v.publishRelease() })
	release.Disabled = v.repo == nil

	file := fyne.NewMenu("File",
		openFolder,
		openRecent,
		refresh,
		fyne.NewMenuItemSeparator(),
		commit,
		initRepo,
		repoIdentity,
		manageRemotes,
		pull,
		push,
		release,
		fyne.NewMenuItemSeparator(),
		compare,
		scanDeps,
		scanAll,
		scanAllCfg,
		lastScanReport,
		auditRepos,
		depAlertRepo,
		depAlertAll,
		depAlertCfg,
		fyne.NewMenuItemSeparator(),
		prefs,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { v.quitApp() }),
	)

	toggleView := fyne.NewMenuItem("Toggle Tracked Files / Folder View", func() { v.toggleMode() })
	legend := fyne.NewMenuItem("Git Status Legend…", func() { v.showStatusLegend() })
	history := fyne.NewMenuItem("Repo History…", func() { v.openHistory() })
	health := fyne.NewMenuItem("Repo Health…", func() { v.openRepoHealth() })
	branches := fyne.NewMenuItem("Branches…", func() { v.showBranches() })
	tags := fyne.NewMenuItem("Tags…", func() { v.showTags() })
	remotes := fyne.NewMenuItem("Remotes…", func() { v.showRemotes() })
	repoConfig := fyne.NewMenuItem("Repo Config…", func() { v.showRepoConfig() })
	reflog := fyne.NewMenuItem("Reflog…", func() { v.openReflog() })
	contributors := fyne.NewMenuItem("Contributors…", func() { v.showContributors() })
	gitLogP := fyne.NewMenuItem("git log -p…", func() { showGitLogPatchView(v.win, v.repoRoot, "") })
	stashes := fyne.NewMenuItem("Stashes…", func() { v.showStashes() })

	view := fyne.NewMenu("View",
		fyne.NewMenuItem("Hide All Windows", func() { hideAllAppWindows(v.app) }),
		fyne.NewMenuItem("Show All Windows", func() { bringAllAppWindowsToFront(v.app, v.win) }),
		fyne.NewMenuItemSeparator(),
		branches,
		contributors,
		gitLogP,
		legend,
		reflog,
		remotes,
		repoConfig,
		health,
		history,
		stashes,
		tags,
		toggleView,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Dark Theme", func() { setDarkTheme(v.app) }),
		fyne.NewMenuItem("Light Theme", func() { setLightTheme(v.app) }),
		fyne.NewMenuItem("System Theme", func() { setSystemTheme(v.app) }),
	)

	help := fyne.NewMenu("Help",
		fyne.NewMenuItem("About", func() { showAbout(v.app) }),
		fyne.NewMenuItem("Check for Updates…", func() { checkForUpdates(v.app) }),
		fyne.NewMenuItem("Help", func() { showHelp(v.app) }),
	)

	return fyne.NewMainMenu(file, view, help)
}

// buildRecentFoldersSubmenu builds the File → Open Recent Folder submenu.
// Includes a sentinel "(no recent folders)" disabled item when the list is
// empty, plus a "Clear recent folders" entry at the bottom when populated.
func (v *explorerView) buildRecentFoldersSubmenu() *fyne.Menu {
	paths := loadRecentFolders(v.app)
	if len(paths) == 0 {
		empty := fyne.NewMenuItem("(no recent folders)", nil)
		empty.Disabled = true
		return fyne.NewMenu("", empty)
	}
	items := make([]*fyne.MenuItem, 0, len(paths)+2)
	for _, p := range paths {
		path := p
		items = append(items, fyne.NewMenuItem(recentMenuLabel(path), func() {
			v.openRecentFolder(path)
		}))
	}
	items = append(items, fyne.NewMenuItemSeparator())
	items = append(items, fyne.NewMenuItem("Clear recent folders", func() {
		clearRecentFolders(v.app)
		v.refreshMenu()
	}))
	return fyne.NewMenu("", items...)
}

// openHistory pops a secondary window with the current repo's commit log and
// a per-commit detail pane. Requires the explorer to be inside a git repo;
// shows an informational dialog otherwise.
func (v *explorerView) openHistory() {
	if v.repo == nil || v.repoRoot == "" {
		dialog.ShowInformation("No repository",
			"Open a folder that is inside a git repository first, then try again.",
			v.win)
		return
	}
	openHistoryWindow(v.app, v.repo, v.repoRoot, v.win, "")
}

// showFileHistory opens a Repo History window pre-filtered to commits that
// touched the given repo-relative path. The user can drop the filter via
// the "Show all history" button in the window itself to widen back to the
// full log without losing the window's other state.
func (v *explorerView) showFileHistory(rel string) {
	if v.repo == nil || v.repoRoot == "" || rel == "" {
		return
	}
	openHistoryWindow(v.app, v.repo, v.repoRoot, v.win, rel)
}

// openReflog pops the read-only Reflog viewer for the current repo. Same
// "must be inside a repo" guard as openHistory / openRepoHealth — the
// reflog is repo-scoped and would otherwise just shell-out-error.
func (v *explorerView) openReflog() {
	if v.repo == nil || v.repoRoot == "" {
		dialog.ShowInformation("No repository",
			"Open a folder that is inside a git repository first, then try again.",
			v.win)
		return
	}
	openReflogWindow(v.app, v.win, v.repo, v.repoRoot)
}

// canBlameSelection reports whether the currently-selected folder-list row
// is a tracked, already-committed file — the same enable rule used for
// "Diff against HEAD" in the right-click context menu. Used by the View ▾
// dropdown to decide whether the Blame… entry is enabled.
func (v *explorerView) canBlameSelection() bool {
	if v.repo == nil || v.repoRoot == "" || v.selectedFileRel == "" {
		return false
	}
	for _, e := range v.entries {
		if e.rel != v.selectedFileRel || e.isDir {
			continue
		}
		if !isTrackedStatus(e.status) {
			return false
		}
		if strings.HasPrefix(e.status, "A") || e.status == "??" {
			return false
		}
		return true
	}
	return false
}

// showBlameForSelection is the View ▾ → Blame… entry point. The right-click
// context menu has its own direct call to openBlameWindow with the row's
// rel; this handles the case where the user reaches Blame through the
// toolbar dropdown instead.
func (v *explorerView) showBlameForSelection() {
	if !v.canBlameSelection() {
		return
	}
	openBlameWindow(v.app, v.win, v.repo, v.repoRoot, v.selectedFileRel)
}

// buildViewDropdownMenu builds the popup menu shown by the toolbar's
// "View ▾" button. Curated to the data views Allan wants quick access to:
// per-file (Blame…) plus repo-level (Branches…, Contributors…, git log -p…,
// Git Status Legend…, Remotes…, Repo Health…, Repo History…, Stashes…,
// Tags…), alphabetised. Excludes toggles already on the toolbar (Tracked
// Files / Folder View, Scan Dependencies), theme switches, and pure
// navigation.
func (v *explorerView) buildViewDropdownMenu() *fyne.Menu {
	blame := fyne.NewMenuItem("Blame…", func() { v.showBlameForSelection() })
	blame.Disabled = !v.canBlameSelection()

	return fyne.NewMenu("",
		blame,
		fyne.NewMenuItem("Branches…", func() { v.showBranches() }),
		fyne.NewMenuItem("Contributors…", func() { v.showContributors() }),
		fyne.NewMenuItem("git log -p…", func() { showGitLogPatchView(v.win, v.repoRoot, "") }),
		fyne.NewMenuItem("Git Status Legend…", func() { v.showStatusLegend() }),
		fyne.NewMenuItem("Reflog…", func() { v.openReflog() }),
		fyne.NewMenuItem("Remotes…", func() { v.showRemotes() }),
		fyne.NewMenuItem("Repo Config…", func() { v.showRepoConfig() }),
		fyne.NewMenuItem("Repo Health…", func() { v.openRepoHealth() }),
		fyne.NewMenuItem("Repo History…", func() { v.openHistory() }),
		fyne.NewMenuItem("Stashes…", func() { v.showStashes() }),
		fyne.NewMenuItem("Tags…", func() { v.showTags() }),
	)
}

// buildRepoDropdownMenu builds the popup menu shown by the toolbar's
// "Repo ▾" button. Holds the v0.8.0 write-ops surface — Initialize
// Repository Here… (enabled when the current folder is NOT yet a repo)
// and Local Repo Identity… (enabled when it IS a repo). Future v0.8.x
// items (Commit, Pull, Push, Remotes management) will join this list.
// buildScanDropdownMenu backs the toolbar "Scan ▾" button — every dependency
// scan action in one place: both engines (local dep-scan + GitHub Dependabot),
// for the current repo or all repos, plus their configuration.
func (v *explorerView) buildScanDropdownMenu() *fyne.Menu {
	thisRepoDep := fyne.NewMenuItem("This repo — Dependabot", func() { v.checkDependabotAlerts() })
	thisRepoDep.Disabled = v.repo == nil

	return fyne.NewMenu("",
		fyne.NewMenuItem("This folder — local dep-scan", func() { v.runDepScan() }),
		thisRepoDep,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("All repos — local dep-scan", func() { v.runDepScanAll() }),
		fyne.NewMenuItem("All repos — Dependabot", func() { v.runDependabotScanAll() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Configure local folders…", func() { showDepScanConfig(v.app, v.win) }),
		fyne.NewMenuItem("Configure Dependabot owners…", func() { showDepAlertConfig(v.app, v.win) }),
		fyne.NewMenuItem("View last local report", func() { v.showLastDepScanReport() }),
	)
}

func (v *explorerView) buildRepoDropdownMenu() *fyne.Menu {
	init := fyne.NewMenuItem("Initialize Repository Here…", func() { v.initRepoHere() })
	init.Disabled = v.repo != nil || v.currentPath == ""

	commit := fyne.NewMenuItem("Commit…", func() { v.commitChanges() })
	commit.Disabled = v.repo == nil

	identity := fyne.NewMenuItem("Local Repo Identity…", func() { v.editRepoIdentity() })
	identity.Disabled = v.repo == nil

	manageRemotes := fyne.NewMenuItem("Manage Remotes…", func() { v.manageRemotes() })
	manageRemotes.Disabled = v.repo == nil

	pull := fyne.NewMenuItem("Pull…", func() { v.pullChanges() })
	pull.Disabled = v.repo == nil

	push := fyne.NewMenuItem("Push…", func() { v.pushChanges() })
	push.Disabled = v.repo == nil

	release := fyne.NewMenuItem("Release…", func() { v.publishRelease() })
	release.Disabled = v.repo == nil

	// Security/scan section (separated), alphabetised within itself.
	auditLocal := fyne.NewMenuItem("Audit Local Repos…", func() { runRepoAudit(v.app, v.win) })
	depRepo := fyne.NewMenuItem("Dependabot: Check This Repo…", func() { v.checkDependabotAlerts() })
	depRepo.Disabled = v.repo == nil
	depAll := fyne.NewMenuItem("Dependabot: Scan All Repos…", func() { v.runDependabotScanAll() })
	scanAllLocal := fyne.NewMenuItem("Scan All Repos (local)…", func() { v.runDepScanAll() })
	scanThisLocal := fyne.NewMenuItem("Scan This Folder (local)…", func() { v.runDepScan() })

	// Alphabetised: Commit, Initialize, Local Repo Identity, Manage Remotes, Pull, Push, Release.
	return fyne.NewMenu("", commit, init, identity, manageRemotes, pull, push, release,
		fyne.NewMenuItemSeparator(),
		auditLocal, depRepo, depAll, scanAllLocal, scanThisLocal)
}

// initRepoHere is the menu entry point for creating a new repository at
// the explorer's current folder. The actual dialog + go-git PlainInit
// call lives in git_management.go; this is just the contextual wrapper
// that supplies the path and a post-success refresh.
func (v *explorerView) initRepoHere() {
	if v.currentPath == "" {
		dialog.ShowInformation("No folder",
			"Open a folder first — initialize creates a .git directory in the currently-open folder.",
			v.win)
		return
	}
	if v.repo != nil {
		dialog.ShowInformation("Already a repository",
			"This folder is already inside a git repository. Use the CLI for re-initialisation.",
			v.win)
		return
	}
	// loadFolder (not refresh) — refresh only re-uses an existing v.repoRoot,
	// but init has just created a .git that wasn't there before. loadFolder
	// re-runs DetectDotGit so the explorer rebinds to the new repo and the
	// header repaints with branch info, commit counts, etc.
	showInitRepoDialog(v.win, v.currentPath, func() {
		v.loadFolder(v.currentPath)
	})
}

// editRepoIdentity is the menu entry point for editing the current
// repo's local user.name / user.email. Requires a loaded repo; the
// underlying dialog (git_management.go) reads the seed values and
// writes via `git config --local`.
func (v *explorerView) editRepoIdentity() {
	if !v.guardRepoLoaded() {
		return
	}
	showRepoIdentityDialog(v.win, v.repoRoot, nil)
}

// manageRemotes is the menu entry point for the Manage Remotes dialog
// (add / edit URL / remove). After each successful mutation, the
// remote-sync indicator in the header is refreshed so the new state
// surfaces immediately.
func (v *explorerView) manageRemotes() {
	if !v.guardRepoLoaded() {
		return
	}
	showManageRemotesDialog(v.win, v.repo, v.repoRoot, func() {
		v.refreshRemoteSync()
	})
}

// commitChanges opens the commit composer. After a successful commit,
// a full refresh repaints the header (last-commit subject, clean/dirty
// counts, sync indicator) and the file list (whose status column
// flips from "modified" back to clean for the just-committed paths).
func (v *explorerView) commitChanges() {
	if !v.guardRepoLoaded() {
		return
	}
	showCommitDialog(v.win, v.repoRoot, func() {
		v.refresh()
	})
}

// pushChanges opens the Push dialog for the current branch. The
// dialog drives the operation internally and keeps itself open so the
// user can read git's progress output. The onPushed callback refreshes
// the header's remote-sync indicator on success; onLocalScan fires the
// existing dep-scan when the inline GitHub-vuln alert's "Re-run local
// dep-scan" button is clicked; onCommitNeeded handles the unborn-HEAD
// recovery. The non-fast-forward "pull first (rebase) + retry" rescue is
// handled entirely inside the dialog now, so it needs no callback.
func (v *explorerView) pushChanges() {
	if !v.guardRepoLoaded() {
		return
	}
	showPushDialog(v.win, v.repo, v.repoRoot,
		func() { v.refreshRemoteSync() },
		func() { runDepScanForRepo(v.app, v.win, v.repoRoot) },
		func() { v.commitChanges() },
	)
}

// pullChanges opens the Pull dialog. Requires an upstream — the dialog
// refuses to open and points the user at Push if there isn't one. On
// success the explorer refreshes so the file list, header counts, and
// remote-sync indicator all reflect the new HEAD.
func (v *explorerView) pullChanges() {
	if !v.guardRepoLoaded() {
		return
	}
	showPullDialog(v.win, v.repo, v.repoRoot, func() {
		v.refresh()
	})
}

// publishRelease opens the Release publishing dialog (v0.9.0). The
// dialog handles its own gh-CLI / github-remote pre-flight checks and
// runs the publish asynchronously so the explorer UI stays responsive
// during the upload.
func (v *explorerView) publishRelease() {
	if !v.guardRepoLoaded() {
		return
	}
	showReleaseDialog(v.win, v.repo, v.repoRoot)
}

// openRepoHealth pops the read-only Repo Health dialog (object-database
// statistics + `git fsck` verify summary + copy-paste-ready hints).
// Requires a repo to be loaded; shows an informational dialog otherwise.
func (v *explorerView) openRepoHealth() {
	if v.repo == nil || v.repoRoot == "" {
		dialog.ShowInformation("No repository",
			"Open a folder that is inside a git repository first, then try again.",
			v.win)
		return
	}
	showRepoHealth(v.app, v.win, v.repo, v.repoRoot)
}

// showBranches / showTags / showRemotes pop the corresponding read-only
// listing dialog. All three require a repo; they share the no-repo guard.
func (v *explorerView) showBranches() {
	if !v.guardRepoLoaded() {
		return
	}
	showBranchesDialog(v.repo, v.win, v.repoRoot)
}
func (v *explorerView) showTags() {
	if !v.guardRepoLoaded() {
		return
	}
	showTagsDialog(v.repo, v.win, v.repoRoot)
}
func (v *explorerView) showRemotes() {
	if !v.guardRepoLoaded() {
		return
	}
	showRemotesDialog(v.repo, v.win, v.repoRoot)
}
func (v *explorerView) showRepoConfig() {
	if !v.guardRepoLoaded() {
		return
	}
	showRepoConfigDialog(v.repo, v.win, v.repoRoot)
}
func (v *explorerView) showContributors() {
	if !v.guardRepoLoaded() {
		return
	}
	showContributorsDialog(v.repo, v.win, v.repoRoot)
}
func (v *explorerView) showStashes() {
	if !v.guardRepoLoaded() {
		return
	}
	showStashesDialog(v.repo, v.win, v.repoRoot)
}

func (v *explorerView) guardRepoLoaded() bool {
	if v.repo == nil || v.repoRoot == "" {
		dialog.ShowInformation("No repository",
			"Open a folder that is inside a git repository first, then try again.",
			v.win)
		return false
	}
	return true
}

// runDepScan launches the dep-scan vulnerability scanner against the current
// folder (whether or not it's a git repo — dep-scan walks for manifests so
// any project root works). The script is resolved from the repo's own copy
// or the user's ~/.claude/skills/dep-scan/ install; results are rendered in
// a scrollable dialog as the rich-text markdown that dep-scan produces.
func (v *explorerView) runDepScan() {
	if v.currentPath == "" {
		dialog.ShowInformation("No folder",
			"Open a folder first — dep-scan walks the chosen folder for dependency manifests (go.mod, package.json, requirements.txt, etc.).",
			v.win)
		return
	}
	runDepScanForRepo(v.app, v.win, v.currentPath)
}

// runDepScanAll sweeps every configured repo in one pass, shows the aggregated
// report, and updates the persistent header badge with the result.
func (v *explorerView) runDepScanAll() {
	runDepScanAll(v.app, v.win, func(res depScanAllResult) {
		v.lastDepScan = &res
		v.setDepScanBadge(res.count, res.when)
		v.app.Preferences().SetInt(prefDepScanLastCount, res.count)
		v.app.Preferences().SetInt(prefDepScanLastTime, int(res.when.Unix()))
	})
}

// showLastDepScanReport re-opens the most recent "Scan All Repos" report. The
// report itself is only held for the current session, so after a relaunch
// (badge seeded from preferences) this prompts a fresh sweep instead.
func (v *explorerView) showLastDepScanReport() {
	if v.lastDepScan == nil {
		dialog.ShowConfirm("Scan All Repos",
			"No scan report from this session yet.\n\nRun a sweep now?",
			func(yes bool) {
				if yes {
					v.runDepScanAll()
				}
			}, v.win)
		return
	}
	r := v.lastDepScan
	showDepScanReportDialog(v.app, v.win, r.repoLabel, r.scriptPath, r.elapsed, r.report, nil)
}

// seedDepScanBadge paints the header badge from the last sweep persisted in
// preferences, so it survives across launches. No-op when no sweep has ever
// run (empty badge).
func (v *explorerView) seedDepScanBadge() {
	when := v.app.Preferences().Int(prefDepScanLastTime) // 0 == never scanned
	if when == 0 {
		return
	}
	v.setDepScanBadge(v.app.Preferences().Int(prefDepScanLastCount), time.Unix(int64(when), 0))
}

// setDepScanBadge renders the cross-repo dependency-health badge in the header.
// Unlike the sync/release labels it's a global aggregate, so it does not change
// on repo switch — only after a "Scan All Repos" sweep.
func (v *explorerView) setDepScanBadge(count int, when time.Time) {
	if v.depScanLabel == nil {
		return
	}
	if count <= 0 {
		v.depScanLabel.SetText("  •  ✓ deps clean")
	} else {
		v.depScanLabel.SetText(fmt.Sprintf("  •  ⚠ %d vuln(s)", count))
	}
}

// --- GitHub Dependabot ---------------------------------------------------------

// checkDependabotAlerts fetches open Dependabot alerts for the current repo and
// shows them in the per-repo cards dialog. github.com only this release; a repo
// whose GitHub remote is on a different host gets a friendly not-yet message.
func (v *explorerView) checkDependabotAlerts() {
	if v.repo == nil {
		dialog.ShowInformation("Dependabot", "Open a git repository first.", v.win)
		return
	}
	host, owner, name, ok := findGitHubReleaseTarget(v.repo)
	if !ok {
		// findGitHubReleaseTarget only returns a GHES host when gh is
		// authenticated there, so a GHES repo with an expired token lands
		// here. Surface the re-auth hint if a GHES remote is present.
		if h := firstNonGitHubHost(v.repo); h != "" {
			dialog.ShowInformation("Dependabot",
				fmt.Sprintf("Not authenticated to %s. Run `%s` in a terminal, then retry.", h, authLoginHint(h)),
				v.win)
			return
		}
		dialog.ShowInformation("Dependabot", "This repo has no recognised GitHub remote.", v.win)
		return
	}

	repoRoot := v.repoRoot
	prog := dialog.NewCustom("Dependabot", "Hide", container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Fetching Dependabot alerts for %s/%s…", owner, name)),
		widget.NewProgressBarInfinite(),
	), v.win)
	prog.Show()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), depAlertCallTimeout)
		defer cancel()
		alerts, err := fetchDependabotAlerts(ctx, host, owner, name)
		fyne.Do(func() {
			prog.Hide()
			if err != nil {
				dialog.ShowError(err, v.win)
				return
			}
			showDependabotAlertsDialog(v.win, owner, name, repoRoot, alerts)
		})
	}()
}

// runDependabotScanAll sweeps every configured repo for open Dependabot alerts,
// shows the aggregate view, and updates the persistent header badge.
func (v *explorerView) runDependabotScanAll() {
	runDependabotScanAll(v.app, v.win, func(s depAlertSummary) {
		v.setDepAlertBadge(s.totalAlerts, s.when)
		v.app.Preferences().SetInt(prefDepAlertLastCount, s.totalAlerts)
		v.app.Preferences().SetInt(prefDepAlertLastTime, int(s.when.Unix()))
	})
}

func (v *explorerView) seedDepAlertBadge() {
	when := v.app.Preferences().Int(prefDepAlertLastTime) // 0 == never scanned
	if when == 0 {
		return
	}
	v.setDepAlertBadge(v.app.Preferences().Int(prefDepAlertLastCount), time.Unix(int64(when), 0))
}

// setDepAlertBadge renders the GitHub-Dependabot badge in the header. Distinct
// from the local dep-scan badge so the two sources read independently.
func (v *explorerView) setDepAlertBadge(count int, when time.Time) {
	if v.depAlertLabel == nil {
		return
	}
	if count <= 0 {
		v.depAlertLabel.SetText("  •  🛡 alerts clear")
	} else {
		v.depAlertLabel.SetText(fmt.Sprintf("  •  🛡 %d alert(s)", count))
	}
}

// dailyScanScheduler runs the once-daily background Dependabot sweep. It owns a
// single goroutine; closing done stops it (idempotent, mirroring repoWatcher).
type dailyScanScheduler struct {
	done chan struct{}
}

const dailyScanCheckInterval = 5 * time.Minute

// startDailyScanScheduler (re)starts the background sweep loop. Safe to call
// repeatedly — it stops any existing loop first, so a prefs change just swaps
// schedules. A short settle delay keeps the sweep off the launch critical path;
// thereafter it re-checks every few minutes (so a machine waking after the
// configured slot still runs the day's sweep — see dueForDailyScan).
func (v *explorerView) startDailyScanScheduler() {
	v.stopDailyScanScheduler()
	s := &dailyScanScheduler{done: make(chan struct{})}
	v.dailyScan = s

	go func() {
		settle := time.NewTimer(20 * time.Second)
		defer settle.Stop()
		ticker := time.NewTicker(dailyScanCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-settle.C:
				v.maybeRunDailyScan()
			case <-ticker.C:
				v.maybeRunDailyScan()
			}
		}
	}()
}

func (v *explorerView) stopDailyScanScheduler() {
	s := v.dailyScan
	if s == nil {
		return
	}
	v.dailyScan = nil
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// maybeRunDailyScan runs the headless Dependabot sweep when one is due, then
// updates + persists the badge on the UI thread. Runs on the scheduler
// goroutine; the network/gh work stays off the UI thread.
func (v *explorerView) maybeRunDailyScan() {
	p := v.app.Preferences()
	if !dueForDailyScan(
		p.Bool(prefDailyDepAlertEnabled),
		p.StringWithFallback(prefDailyDepAlertTime, "09:00"),
		p.String(prefDailyDepAlertLastRun),
		time.Now(),
	) {
		return
	}
	total, ok := sweepDependabotAlerts(v.app)
	now := time.Now()
	// Record the run date regardless of ok, so a no-op day (no targets / all
	// errored) doesn't retry every 5 minutes; the badge only updates on ok.
	p.SetString(prefDailyDepAlertLastRun, now.Format("2006-01-02"))
	if !ok {
		return
	}
	fyne.Do(func() {
		v.setDepAlertBadge(total, now)
		p.SetInt(prefDepAlertLastCount, total)
		p.SetInt(prefDepAlertLastTime, int(now.Unix()))
	})
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
		{"submodule", "A separate git repository embedded in this one (declared in .gitmodules). Click the row to descend into it as its own repo; it'll appear in your recent folders list."},
		{"contains submodule", "Not a submodule itself, but somewhere inside this directory there's one (e.g. a vendor/ folder holding a submodule). Click through to find it."},
		{"modified (N) / untracked (N) / conflict (N)", "Directory rollup — N is the total number of non-clean files anywhere in this directory's subtree. The headline label is the highest-severity category present (conflict > modified > untracked); the count covers all non-clean files, not just that category. Ignored files are not rolled up."},
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
	// The tray menu is built once at startup (rebuilding the system tray
	// menu repeatedly on GLFW/macOS leaks goroutines — see the comment on
	// the diff window's buildTrayMenu). So this Recent Folders submenu
	// captures the recents-list-as-of-app-launch and stays static for the
	// session. For the live-updating list, use the menu bar's File → Open
	// Recent Folder, which rebuilds after every folder open.
	trayRecentFolders := fyne.NewMenuItem("Open Recent Folder", nil)
	trayRecentFolders.ChildMenu = v.buildRecentFoldersSubmenu()

	return fyne.NewMenu(appName,
		fyne.NewMenuItem("Hide All Windows", func() { hideAllAppWindows(v.app) }),
		fyne.NewMenuItem("Show All Windows", func() { bringAllAppWindowsToFront(v.app, v.win) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Compare Two Files…", func() { openDiffWindow(v.app, false) }),
		fyne.NewMenuItem("Open Folder…", func() { v.browseFolder() }),
		trayRecentFolders,
		fyne.NewMenuItem("Preferences…", func() { showPreferences(v.app, nil) }),
		fyne.NewMenuItem("Scan Dependencies…", func() { v.runDepScan() }),
		fyne.NewMenuItem("Scan All Repos…", func() { v.runDepScanAll() }),
		fyne.NewMenuItem("Configure Repos to Scan…", func() { showDepScanConfig(v.app, v.win) }),
		fyne.NewMenuItem("Audit Local Repos…", func() { runRepoAudit(v.app, v.win) }),
		fyne.NewMenuItem("Dependabot: Check This Repo…", func() { v.checkDependabotAlerts() }),
		fyne.NewMenuItem("Dependabot: Scan All Repos…", func() { v.runDependabotScanAll() }),
		fyne.NewMenuItem("Dependabot: Configure Owners…", func() { showDepAlertConfig(v.app, v.win) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Branches…", func() { v.showBranches() }),
		fyne.NewMenuItem("Contributors…", func() { v.showContributors() }),
		fyne.NewMenuItem("git log -p…", func() { showGitLogPatchView(v.win, v.repoRoot, "") }),
		fyne.NewMenuItem("Git Status Legend…", func() { v.showStatusLegend() }),
		fyne.NewMenuItem("Reflog…", func() { v.openReflog() }),
		fyne.NewMenuItem("Remotes…", func() { v.showRemotes() }),
		fyne.NewMenuItem("Repo Config…", func() { v.showRepoConfig() }),
		fyne.NewMenuItem("Repo Health…", func() { v.openRepoHealth() }),
		fyne.NewMenuItem("Repo History…", func() { v.openHistory() }),
		fyne.NewMenuItem("Stashes…", func() { v.showStashes() }),
		fyne.NewMenuItem("Tags…", func() { v.showTags() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Dark Theme", func() { setDarkTheme(v.app) }),
		fyne.NewMenuItem("Light Theme", func() { setLightTheme(v.app) }),
		fyne.NewMenuItem("System Theme", func() { setSystemTheme(v.app) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("About", func() { showAbout(v.app) }),
		fyne.NewMenuItem("Check for Updates…", func() { checkForUpdates(v.app) }),
		fyne.NewMenuItem("Help", func() { showHelp(v.app) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { v.quitApp() }),
	)
}
