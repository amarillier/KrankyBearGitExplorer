package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

const historyPageSize = 200

// historyView is the repo-history secondary window: a commit list on the
// left, a per-commit detail pane on the right (subject + file list); clicking
// a file opens a both-sides-read-only diff between this commit's blob and its
// parent's blob using the existing two-pane diff engine.
type historyView struct {
	app      fyne.App
	win      fyne.Window
	repo     *git.Repository
	repoRoot string

	commitIter     object.CommitIter // streaming source for "Load more"
	allCommits     []*object.Commit  // master list, grows as iter yields
	commits        []*object.Commit  // displayed subset (== allCommits when no filter)
	selectedCommit *object.Commit
	changes        object.Changes

	commitList    *widget.List
	fileList      *widget.List
	headerLabel   *widget.Label
	messageLabel  *widget.Label
	hintLabel     *widget.Label
	loadMoreBtn   *ttwidget.Button
	commitCountLb *widget.Label

	// Search-across-history filter. First activation drains the iterator so
	// matching is against the full repo log, not just the loaded page.
	msgEntry    *widget.Entry
	authEntry   *widget.Entry
	msgFilter   string // lowercased substring against full commit message
	authFilter  string // lowercased substring against author name
	filterTimer *time.Timer
	filterMu    sync.Mutex

	// pathFilter, when non-empty, scopes the iterator to commits that
	// touched this repo-relative path. Set by the "Show history for this
	// file…" right-click action and toggled via the in-window button.
	//
	// previousPathFilter remembers the most-recently-active path after
	// the user has clicked "Show all history" so a "Back to file
	// history" button can re-apply it without making the user close and
	// reopen from the explorer.
	pathFilter         string
	previousPathFilter string
	pathFilterRow      *fyne.Container
	pathFilterLabel    *widget.Label
	pathToggleBtn      *ttwidget.Button

	// compareBase, when non-nil, puts the right pane in "compare mode":
	// the detail and file list show what changed between this commit and
	// whichever commit is currently selected (instead of selected-vs-parent),
	// and clicking a file opens a Historical Diff between the two arbitrary
	// commits. Cancel via the banner; replace by picking another base.
	compareBase    *object.Commit
	compareRow     *fyne.Container
	compareLabel   *widget.Label
	pickCompareBtn *ttwidget.Button

	// lastDiffFile remembers the path of the most-recent file-diff opened
	// from this history window. In compare mode, changing the commit
	// selection auto-reopens the diff for this file so the diff window
	// stays in sync with the history view — clicking through commits
	// becomes a "scrub" of the same file across time.
	lastDiffFile string

	// sigStatus maps commit SHA → `git log --format=%G?` status byte
	// (G/B/U/X/Y/R/E/N — see commit_signatures.go). Populated once,
	// asynchronously, after the window opens so the user isn't blocked
	// while git walks the log. Reads from this map happen on the UI
	// thread (row template, selectCommit) — protected by sigMu since
	// the writer goroutine and the UI thread don't otherwise sync.
	sigStatus map[plumbing.Hash]byte
	sigMu     sync.RWMutex
	sigLabel  *widget.Label // detail-pane "Signature: ..." line
}

func openHistoryWindow(a fyne.App, repo *git.Repository, repoRoot string, parent fyne.Window, pathFilter string) *historyView {
	iter, err := openHistoryIter(repo, pathFilter)
	if err != nil {
		if parent != nil {
			dialog.ShowError(fmt.Errorf("open log: %w", err), parent)
		}
		return nil
	}

	v := &historyView{
		app:        a,
		repo:       repo,
		repoRoot:   repoRoot,
		commitIter: iter,
		pathFilter: pathFilter,
	}
	v.loadMoreCommits(historyPageSize)

	w := a.NewWindow(historyWindowTitle(repoRoot, pathFilter))
	v.win = w
	w.SetIcon(resourceKrankyBearNerdPng)
	w.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(v.buildUI()), w.Canvas()))
	w.Resize(fyne.NewSize(1000, 700))
	w.SetCloseIntercept(func() {
		v.filterMu.Lock()
		if v.filterTimer != nil {
			v.filterTimer.Stop()
			v.filterTimer = nil
		}
		v.filterMu.Unlock()
		if v.commitIter != nil {
			v.commitIter.Close()
			v.commitIter = nil
		}
		unregisterRepoChildWindow(w)
		fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
		windowHide(w)
	})
	registerRepoChildWindow(w)
	windowShow(w)
	v.refreshHeader()
	go v.loadSignaturesAsync()
	return v
}

// loadSignaturesAsync runs `git log --format=%H %G?` in the background
// and refreshes the commit list + currently-selected detail pane when
// the result lands. Falls back to "no badges" silently when git isn't
// on PATH — the history window remains fully functional, just without
// signature decoration.
func (v *historyView) loadSignaturesAsync() {
	m := gatherCommitSignatures(v.repoRoot)
	fyne.Do(func() {
		v.sigMu.Lock()
		v.sigStatus = m
		v.sigMu.Unlock()
		if v.commitList != nil {
			v.commitList.Refresh()
		}
		// Re-render the detail pane so its Signature line picks up the
		// status for the currently-selected commit.
		if v.selectedCommit != nil {
			v.refreshSignatureLabel(v.selectedCommit)
		}
	})
}

// signatureFor returns the status byte for sha (0 if not yet loaded or
// not in the map). Locked read so it's safe to call from the UI thread
// while loadSignaturesAsync is still running.
func (v *historyView) signatureFor(sha plumbing.Hash) byte {
	v.sigMu.RLock()
	defer v.sigMu.RUnlock()
	return v.sigStatus[sha]
}

// refreshSignatureLabel updates the detail-pane's signature line based
// on the given commit. Hides the label (empty text) for unsigned
// commits so a repo with no signed history doesn't show a perpetual
// "Signature: unsigned" line.
func (v *historyView) refreshSignatureLabel(c *object.Commit) {
	if v.sigLabel == nil || c == nil {
		return
	}
	v.sigLabel.SetText(signatureHumanLabel(v.signatureFor(c.Hash)))
}

// openHistoryIter builds the commit iterator for the history window. When
// pathFilter is non-empty, the iterator yields only commits that touched
// that repo-relative path (go-git's LogOptions.FileName, matched against
// the path; for a literal filename this is an exact path match).
func openHistoryIter(repo *git.Repository, pathFilter string) (object.CommitIter, error) {
	opts := &git.LogOptions{Order: git.LogOrderCommitterTime}
	if pathFilter != "" {
		p := pathFilter
		opts.FileName = &p
	}
	return repo.Log(opts)
}

// historyWindowTitle composes the window title, including the path filter
// when active so "File History: foo.go" reads distinctly from a general
// "Repo History: …" window.
func historyWindowTitle(repoRoot, pathFilter string) string {
	if pathFilter != "" {
		return fmt.Sprintf("%s — File History: %s — %s", appName, filepath.Base(repoRoot), pathFilter)
	}
	return fmt.Sprintf("%s — Repo History: %s", appName, filepath.Base(repoRoot))
}

// loadMoreCommits pulls up to n more commits from the iterator into
// v.allCommits, then re-applies the active filter so v.commits stays in sync.
// When the iterator is exhausted, it's closed and nilled so the "Load more"
// button can disable itself.
func (v *historyView) loadMoreCommits(n int) {
	if v.commitIter == nil {
		v.applyFilter()
		return
	}
	for i := 0; i < n; i++ {
		c, err := v.commitIter.Next()
		if err != nil { // includes io.EOF
			v.commitIter.Close()
			v.commitIter = nil
			break
		}
		v.allCommits = append(v.allCommits, c)
	}
	v.applyFilter()
}

// loadAllCommits drains the iterator to completion. Used on first activation
// of the search filter so the filter sees the full repo log, not just the
// currently-loaded page.
func (v *historyView) loadAllCommits() {
	if v.commitIter == nil {
		return
	}
	for {
		c, err := v.commitIter.Next()
		if err != nil { // includes io.EOF
			v.commitIter.Close()
			v.commitIter = nil
			return
		}
		v.allCommits = append(v.allCommits, c)
	}
}

// applyFilter rebuilds v.commits from v.allCommits + the active filter state
// and refreshes the dependent widgets. When no filter is active, v.commits
// shares allCommits' backing array — cheap.
func (v *historyView) applyFilter() {
	if v.msgFilter == "" && v.authFilter == "" {
		v.commits = v.allCommits
	} else {
		out := make([]*object.Commit, 0, len(v.allCommits))
		for _, c := range v.allCommits {
			if v.msgFilter != "" && !strings.Contains(strings.ToLower(c.Message), v.msgFilter) {
				continue
			}
			if v.authFilter != "" && !strings.Contains(strings.ToLower(c.Author.Name), v.authFilter) {
				continue
			}
			out = append(out, c)
		}
		v.commits = out
	}
	if v.commitList != nil {
		v.commitList.Refresh()
	}
	v.refreshHeader()
}

// setPathFilter transitions the window between path-filtered and unfiltered
// history without closing it. Passing "" drops the filter (and remembers
// what was there so "Back to file history" can re-apply it); passing a path
// applies the filter. No-op when the requested state already holds.
//
// Either direction: tear down the current iterator, reset the loaded
// commits + the message/author search fields, build a fresh iterator with
// the new filter, and refresh the path-filter row + window title.
func (v *historyView) setPathFilter(path string) {
	if path == v.pathFilter {
		return
	}
	if v.pathFilter != "" {
		v.previousPathFilter = v.pathFilter
	}
	if v.commitIter != nil {
		v.commitIter.Close()
		v.commitIter = nil
	}
	v.pathFilter = path
	v.allCommits = nil
	v.commits = nil

	v.filterMu.Lock()
	if v.filterTimer != nil {
		v.filterTimer.Stop()
		v.filterTimer = nil
	}
	v.filterMu.Unlock()
	v.msgFilter = ""
	v.authFilter = ""
	if v.msgEntry != nil {
		v.msgEntry.SetText("")
	}
	if v.authEntry != nil {
		v.authEntry.SetText("")
	}

	iter, err := openHistoryIter(v.repo, path)
	if err != nil {
		dialog.ShowError(fmt.Errorf("open log: %w", err), v.win)
		return
	}
	v.commitIter = iter
	v.loadMoreCommits(historyPageSize)

	if v.win != nil {
		v.win.SetTitle(historyWindowTitle(v.repoRoot, path))
	}
	v.refreshPathFilterRow()
}

// refreshPathFilterRow drives the label + button to match the current
// pathFilter / previousPathFilter state. Three cases:
//
//   - active filter:  "File history: <path>" + "Show all history" button.
//   - filter dropped, but a previous path is remembered: shows the
//     breadcrumb "Last file: <path>" + "Back to file history" button.
//   - no filter, no breadcrumb: row hidden entirely (the original
//     File-menu-opened-history shape).
func (v *historyView) refreshPathFilterRow() {
	if v.pathFilterRow == nil || v.pathFilterLabel == nil || v.pathToggleBtn == nil {
		return
	}
	switch {
	case v.pathFilter != "":
		v.pathFilterLabel.SetText("File history: " + v.pathFilter)
		v.pathToggleBtn.SetText("Show all history")
		v.pathToggleBtn.SetToolTip("Drop the file-history filter; the file stays remembered so you can flip back.")
		v.pathFilterRow.Show()
	case v.previousPathFilter != "":
		v.pathFilterLabel.SetText("Last file: " + v.previousPathFilter)
		v.pathToggleBtn.SetText("Back to file history")
		v.pathToggleBtn.SetToolTip("Re-apply the file-history filter for " + v.previousPathFilter)
		v.pathFilterRow.Show()
	default:
		v.pathFilterRow.Hide()
	}
}

// scheduleFilter debounces filter-entry keystrokes by ~150ms so a long typed
// query doesn't re-walk the commit list on every character.
func (v *historyView) scheduleFilter() {
	v.filterMu.Lock()
	defer v.filterMu.Unlock()
	if v.filterTimer != nil {
		v.filterTimer.Stop()
	}
	v.filterTimer = time.AfterFunc(150*time.Millisecond, func() {
		fyne.Do(func() { v.runFilterFromInputs() })
	})
}

// runFilterFromInputs reads the current entry values, drains the iterator on
// first activation, and applies the filter.
func (v *historyView) runFilterFromInputs() {
	msg := strings.ToLower(strings.TrimSpace(v.msgEntry.Text))
	auth := strings.ToLower(strings.TrimSpace(v.authEntry.Text))
	activating := (msg != "" || auth != "") && v.commitIter != nil
	v.msgFilter = msg
	v.authFilter = auth
	if activating {
		v.loadAllCommits()
	}
	v.applyFilter()
}

func (v *historyView) buildUI() fyne.CanvasObject {
	// --- left pane: commit list -------------------------------------------
	v.commitList = widget.NewList(
		func() int { return len(v.commits) },
		func() fyne.CanvasObject {
			subject := widget.NewLabel("")
			subject.TextStyle = fyne.TextStyle{Bold: true}
			subject.Truncation = fyne.TextTruncateEllipsis
			meta := widget.NewLabel("")
			meta.Truncation = fyne.TextTruncateEllipsis
			// Signature badge sits in a left-side gutter so the SHA
			// stays unencumbered; widget.Label is fine for unicode
			// glyphs and inherits theme colours so dark/light mode
			// stays consistent. Monospaced label keeps the gutter
			// width stable when signed and unsigned rows mix.
			badge := widget.NewLabel(" ")
			badge.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
			return container.NewBorder(nil, nil, badge, nil, container.NewVBox(subject, meta))
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(v.commits) {
				return
			}
			c := v.commits[id]
			border := o.(*fyne.Container)
			badge := border.Objects[1].(*widget.Label)
			box := border.Objects[0].(*fyne.Container)
			subject := box.Objects[0].(*widget.Label)
			meta := box.Objects[1].(*widget.Label)
			subject.SetText(firstLine(c.Message))
			short := c.Hash.String()
			if len(short) > 7 {
				short = short[:7]
			}
			meta.SetText(fmt.Sprintf("%s  •  %s  •  %s", short, c.Author.Name, humanRelativeTime(c.Author.When)))
			badge.SetText(signatureBadge(v.signatureFor(c.Hash)))
		},
	)
	v.commitList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(v.commits) {
			return
		}
		v.selectCommit(v.commits[id])
	}

	v.commitCountLb = widget.NewLabel("")
	v.loadMoreBtn = ttwidget.NewButton("Load more commits", func() {
		v.loadMoreCommits(historyPageSize)
	})
	v.loadMoreBtn.SetToolTip(fmt.Sprintf("Load the next %d commits", historyPageSize))
	v.loadMoreBtn.Importance = widget.LowImportance

	v.msgEntry = widget.NewEntry()
	v.msgEntry.SetPlaceHolder("Message contains…")
	v.msgEntry.OnChanged = func(string) { v.scheduleFilter() }
	v.authEntry = widget.NewEntry()
	v.authEntry.SetPlaceHolder("Author contains…")
	v.authEntry.OnChanged = func(string) { v.scheduleFilter() }
	filterRow := container.NewGridWithColumns(2, v.msgEntry, v.authEntry)

	v.pathFilterLabel = widget.NewLabel("")
	v.pathFilterLabel.TextStyle = fyne.TextStyle{Italic: true}
	v.pathFilterLabel.Truncation = fyne.TextTruncateEllipsis
	// Single toggle button whose label and action flip based on state:
	// while a path is active the button drops it ("Show all history");
	// after dropping, the previous path is remembered and the button
	// flips to "Back to file history" so the user can re-apply with one
	// click instead of right-clicking from the explorer again.
	v.pathToggleBtn = ttwidget.NewButton("Show all history", func() {
		switch {
		case v.pathFilter != "":
			v.setPathFilter("")
		case v.previousPathFilter != "":
			v.setPathFilter(v.previousPathFilter)
		}
	})
	v.pathToggleBtn.Importance = widget.LowImportance
	v.pathFilterRow = container.NewBorder(nil, nil, nil, v.pathToggleBtn, v.pathFilterLabel)
	v.refreshPathFilterRow()

	v.compareLabel = widget.NewLabel("")
	v.compareLabel.TextStyle = fyne.TextStyle{Italic: true}
	v.compareLabel.Truncation = fyne.TextTruncateEllipsis
	cancelCompareBtn := ttwidget.NewButtonWithIcon("Cancel compare", theme.CancelIcon(), func() { v.clearCompareBase() })
	cancelCompareBtn.SetToolTip("Drop the compare base; the right pane goes back to showing per-commit detail vs parent.")
	cancelCompareBtn.Importance = widget.LowImportance
	v.compareRow = container.NewBorder(nil, nil, nil, cancelCompareBtn, v.compareLabel)
	v.compareRow.Hide()

	topBar := container.NewVBox(v.compareRow, v.pathFilterRow, filterRow)
	leftFooter := container.NewBorder(nil, nil, v.commitCountLb, v.loadMoreBtn, nil)
	leftPane := container.NewBorder(topBar, leftFooter, nil, nil, v.commitList)

	// --- right pane: commit detail + file list ----------------------------
	v.headerLabel = widget.NewLabel("(select a commit)")
	v.headerLabel.TextStyle = fyne.TextStyle{Bold: true}
	v.headerLabel.Truncation = fyne.TextTruncateEllipsis

	v.messageLabel = widget.NewLabel("")
	v.messageLabel.Wrapping = fyne.TextWrapWord

	v.hintLabel = widget.NewLabel("")
	v.hintLabel.TextStyle = fyne.TextStyle{Italic: true}

	v.sigLabel = widget.NewLabel("")
	v.sigLabel.TextStyle = fyne.TextStyle{Italic: true}
	v.sigLabel.Truncation = fyne.TextTruncateEllipsis

	v.fileList = widget.NewList(
		func() int { return len(v.changes) },
		func() fyne.CanvasObject {
			lbl := widget.NewLabel("")
			lbl.Truncation = fyne.TextTruncateEllipsis
			return lbl
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(v.changes) {
				return
			}
			ch := v.changes[id]
			lbl := o.(*widget.Label)
			act, _ := ch.Action()
			letter := actionLetter(act)
			path := changePath(ch)
			lbl.SetText(fmt.Sprintf("[%s]  %s", letter, path))
		},
	)
	v.fileList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(v.changes) {
			return
		}
		v.openFileDiff(v.changes[id])
		// Unselect so a re-click on the same row re-opens the diff.
		v.fileList.UnselectAll()
	}

	v.pickCompareBtn = ttwidget.NewButtonWithIcon("Pick as compare base", theme.ConfirmIcon(), func() {
		if v.selectedCommit != nil {
			v.setCompareBase(v.selectedCommit)
		}
	})
	v.pickCompareBtn.SetToolTip("Mark this commit as the compare base. Selecting another commit will then show the diff between the two.")
	v.pickCompareBtn.Importance = widget.MediumImportance
	v.pickCompareBtn.Disable()

	detailHeader := container.NewVBox(
		v.headerLabel,
		v.sigLabel,
		v.messageLabel,
		v.hintLabel,
		container.NewHBox(v.pickCompareBtn),
		widget.NewSeparator(),
	)
	rightPane := container.NewBorder(detailHeader, nil, nil, nil, v.fileList)

	// --- split ------------------------------------------------------------
	split := container.NewHSplit(leftPane, rightPane)
	split.SetOffset(0.45)
	return split
}

func (v *historyView) refreshHeader() {
	if v.commitCountLb == nil {
		return
	}
	filterActive := v.msgFilter != "" || v.authFilter != ""
	if filterActive {
		v.commitCountLb.SetText(fmt.Sprintf("%d of %d commits match", len(v.commits), len(v.allCommits)))
	} else {
		more := ""
		if v.commitIter == nil {
			more = " (all loaded)"
		}
		v.commitCountLb.SetText(fmt.Sprintf("%d commits loaded%s", len(v.commits), more))
	}
	if v.loadMoreBtn != nil {
		if v.commitIter == nil || filterActive {
			v.loadMoreBtn.Disable()
		} else {
			v.loadMoreBtn.Enable()
		}
	}
}

func (v *historyView) selectCommit(c *object.Commit) {
	v.selectedCommit = c
	v.refreshPickCompareBtn()
	v.refreshSignatureLabel(c)

	short := shortSHA(c.Hash.String())

	// Compare mode: render base→selected diff instead of selected-vs-parent.
	// If the selected commit IS the compare base, show a hint to pick a
	// different commit (the empty self-diff isn't useful).
	if v.compareBase != nil {
		if v.compareBase.Hash == c.Hash {
			v.headerLabel.SetText(fmt.Sprintf("%s  •  (this is the compare base)", short))
			v.messageLabel.SetText(strings.TrimSpace(c.Message))
			v.hintLabel.SetText("Pick a different commit to see what changed between them.")
			v.changes = nil
			v.fileList.Refresh()
			v.fileList.UnselectAll()
			return
		}
		baseShort := shortSHA(v.compareBase.Hash.String())
		v.headerLabel.SetText(fmt.Sprintf("Compare: %s  →  %s", baseShort, short))
		v.messageLabel.SetText(fmt.Sprintf("%s  ⟶  %s",
			firstLine(v.compareBase.Message), firstLine(c.Message)))
		v.changes = nil
		if baseTree, err := v.compareBase.Tree(); err == nil {
			if ct, err := c.Tree(); err == nil {
				if changes, err := baseTree.Diff(ct); err == nil {
					v.changes = v.scopeChangesToPathFilter(changes)
				}
			}
		}
		hint := fmt.Sprintf("%d file(s) changed between %s and %s — click any to open the diff.",
			len(v.changes), baseShort, short)
		if v.pathFilter != "" {
			hint = fmt.Sprintf("%d change(s) to %s between %s and %s — click to open the diff.",
				len(v.changes), v.pathFilter, baseShort, short)
		}
		v.hintLabel.SetText(hint)
		v.fileList.Refresh()
		v.fileList.UnselectAll()
		v.maybeReopenLastDiff()
		return
	}

	// Normal mode: per-commit detail vs parent.
	v.headerLabel.SetText(fmt.Sprintf("%s  •  %s <%s>  •  %s",
		short, c.Author.Name, c.Author.Email,
		c.Author.When.Format("2006-01-02 15:04 MST"),
	))
	v.messageLabel.SetText(strings.TrimSpace(c.Message))

	v.changes = nil
	if c.NumParents() == 0 {
		// Initial commit — synthesise an "all files added" change list by
		// walking the commit's tree.
		var initial object.Changes
		if tree, err := c.Tree(); err == nil {
			_ = tree.Files().ForEach(func(f *object.File) error {
				initial = append(initial, &object.Change{
					To: object.ChangeEntry{
						Name: f.Name,
						TreeEntry: object.TreeEntry{
							Name: f.Name,
							Mode: f.Mode,
							Hash: f.Hash,
						},
					},
				})
				return nil
			})
		}
		v.changes = v.scopeChangesToPathFilter(initial)
		v.hintLabel.SetText(fmt.Sprintf("%d file(s) in initial commit — click any to view its contents.", len(v.changes)))
	} else {
		parent, err := c.Parent(0)
		if err == nil && parent != nil {
			pt, perr := parent.Tree()
			ct, cerr := c.Tree()
			if perr == nil && cerr == nil {
				changes, derr := pt.Diff(ct)
				if derr == nil {
					v.changes = v.scopeChangesToPathFilter(changes)
				}
			}
		}
		hint := fmt.Sprintf("%d file(s) changed vs parent — click any to open the diff.", len(v.changes))
		if v.pathFilter != "" {
			hint = fmt.Sprintf("%d change(s) to %s vs parent — click to open the diff.", len(v.changes), v.pathFilter)
		}
		v.hintLabel.SetText(hint)
	}
	v.fileList.Refresh()
	v.fileList.UnselectAll()
}

// scopeChangesToPathFilter narrows a change set to just the entries matching
// the active pathFilter (file-history mode). When no path filter is active,
// the input is returned unchanged so this is safe to call unconditionally.
func (v *historyView) scopeChangesToPathFilter(in object.Changes) object.Changes {
	if v.pathFilter == "" {
		return in
	}
	out := make(object.Changes, 0, 1)
	for _, ch := range in {
		if changePath(ch) == v.pathFilter {
			out = append(out, ch)
		}
	}
	return out
}

// maybeReopenLastDiff auto-reopens the most-recently-viewed file diff when
// the user changes commit selection in compare mode. Only fires when:
//   - compare mode is active
//   - a previous diff was opened from this history window (lastDiffFile set)
//   - a secondary diff window is currently registered (the user hasn't
//     closed it manually — don't ambush them by reopening)
//   - the remembered file is present in the new change set
//
// The "Keep only one diff window" preference does the rest: the old diff
// closes as the new one opens, so the diff window appears to "follow" the
// selection across commits.
func (v *historyView) maybeReopenLastDiff() {
	if v.compareBase == nil || v.lastDiffFile == "" || len(secondaryDiffWindows) == 0 {
		return
	}
	for _, ch := range v.changes {
		if changePath(ch) == v.lastDiffFile {
			v.openFileDiff(ch)
			return
		}
	}
}

// setCompareBase marks a commit as the left side of a two-commit comparison.
// Replaces any existing base. Re-renders the right pane against whichever
// commit is currently selected so the diff appears immediately.
func (v *historyView) setCompareBase(c *object.Commit) {
	if c == nil {
		return
	}
	v.compareBase = c
	v.refreshCompareRow()
	v.refreshPickCompareBtn()
	if v.selectedCommit != nil {
		v.selectCommit(v.selectedCommit)
	}
}

// clearCompareBase exits compare mode and re-renders the right pane as the
// usual per-commit detail vs parent for whichever commit is selected.
func (v *historyView) clearCompareBase() {
	if v.compareBase == nil {
		return
	}
	v.compareBase = nil
	v.refreshCompareRow()
	v.refreshPickCompareBtn()
	if v.selectedCommit != nil {
		v.selectCommit(v.selectedCommit)
	}
}

// refreshCompareRow updates the banner above the commit list. Visible only
// while a compare base is set; the label shows the base's short SHA and
// subject so it's identifiable at a glance.
func (v *historyView) refreshCompareRow() {
	if v.compareRow == nil || v.compareLabel == nil {
		return
	}
	if v.compareBase == nil {
		v.compareRow.Hide()
		return
	}
	short := shortSHA(v.compareBase.Hash.String())
	v.compareLabel.SetText(fmt.Sprintf("Compare base: %s — %s", short, firstLine(v.compareBase.Message)))
	v.compareRow.Show()
}

// refreshPickCompareBtn keeps the detail-pane button in sync with whatever
// is selected: enabled when a commit is selected, disabled and relabelled
// when the selection is the active compare base (avoids a no-op pick).
func (v *historyView) refreshPickCompareBtn() {
	if v.pickCompareBtn == nil {
		return
	}
	if v.selectedCommit == nil {
		v.pickCompareBtn.SetText("Pick as compare base")
		v.pickCompareBtn.Disable()
		return
	}
	if v.compareBase != nil && v.compareBase.Hash == v.selectedCommit.Hash {
		v.pickCompareBtn.SetText("(this is the compare base)")
		v.pickCompareBtn.Disable()
		return
	}
	v.pickCompareBtn.SetText("Pick as compare base")
	v.pickCompareBtn.Enable()
}

// shortSHA returns the first 7 characters of a SHA, defensively guarded so
// it never panics on short inputs.
func shortSHA(s string) string {
	if len(s) <= 7 {
		return s
	}
	return s[:7]
}

func (v *historyView) openFileDiff(ch *object.Change) {
	if v.repo == nil || v.selectedCommit == nil {
		return
	}
	v.lastDiffFile = changePath(ch)
	oldContent, oldErr := readBlobIfText(v.repo, ch.From.TreeEntry.Hash)
	newContent, newErr := readBlobIfText(v.repo, ch.To.TreeEntry.Hash)

	if oldErr == errBlobBinary || newErr == errBlobBinary {
		dialog.ShowInformation("Binary file",
			"This file looks binary (contains NUL bytes). Skipping the text diff to avoid garbage output.",
			v.win)
		return
	}

	short := shortSHA(v.selectedCommit.Hash.String())
	commitDate := v.selectedCommit.Author.When.Format("2006-01-02")
	commitLabel := fmt.Sprintf("%s %s", short, commitDate)
	commitSubject := firstLine(v.selectedCommit.Message)

	// Left-side commit: in compare mode it's the explicitly-picked base; in
	// normal mode it's the parent of the selected commit. The SHA + date
	// label keeps two historical diffs distinguishable even when they share
	// a file path. "(root)" stands in for the synthetic pre-history when the
	// selected commit is initial; in that case the left subtitle stays empty.
	var leftCommit *object.Commit
	if v.compareBase != nil && v.compareBase.Hash != v.selectedCommit.Hash {
		leftCommit = v.compareBase
	} else if v.selectedCommit.NumParents() > 0 {
		if p, err := v.selectedCommit.Parent(0); err == nil {
			leftCommit = p
		}
	}

	leftLabel := "(root)"
	leftSubject := ""
	if leftCommit != nil {
		ls := shortSHA(leftCommit.Hash.String())
		leftLabel = fmt.Sprintf("%s %s", ls, leftCommit.Author.When.Format("2006-01-02"))
		leftSubject = firstLine(leftCommit.Message)
	}

	oldName := ch.From.Name
	newName := ch.To.Name
	if oldName == "" {
		oldName = newName
	}
	if newName == "" {
		newName = oldName
	}

	leftP := fmt.Sprintf("%s: %s", leftLabel, oldName)
	rightP := fmt.Sprintf("%s: %s", commitLabel, newName)
	openDiffWindowWithPreload(v.app, false,
		leftP, oldContent, rightP, newContent,
		leftSubject, commitSubject,
		true, // leftReadOnly — left blob (parent or compare base) is immutable
		true, // rightReadOnly — selected commit's blob is immutable
	)
}

// readBlobIfText fetches the blob for hash and returns its contents, or:
//   - "" with nil error if the hash is the zero hash (file did not exist on
//     this side, e.g. an Added file's "From" or a Deleted file's "To");
//   - errBlobBinary if the blob looks binary (NUL byte in the first 8 KB);
//   - some other error if the blob can't be read.
func readBlobIfText(repo *git.Repository, hash plumbing.Hash) (string, error) {
	if hash.IsZero() {
		return "", nil
	}
	blob, err := repo.BlobObject(hash)
	if err != nil {
		return "", err
	}
	r, err := blob.Reader()
	if err != nil {
		return "", err
	}
	defer r.Close()
	const sniffN = 8192
	buf := make([]byte, sniffN)
	n, _ := io.ReadFull(r, buf)
	if n > 0 {
		for i := 0; i < n; i++ {
			if buf[i] == 0 {
				return "", errBlobBinary
			}
		}
	}
	// Now read the rest.
	rest, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	all := append(buf[:n], rest...)
	return string(all), nil
}

// errBlobBinary signals a blob that contains NUL bytes in its sniff window —
// the line-oriented diff engine would render garbage for such content, so we
// surface a dialog instead.
var errBlobBinary = fmt.Errorf("binary blob")

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimRight(s[:idx], " \t\r")
	}
	return strings.TrimRight(s, " \t\r")
}

func actionLetter(a merkletrie.Action) string {
	switch a {
	case merkletrie.Insert:
		return "A"
	case merkletrie.Delete:
		return "D"
	case merkletrie.Modify:
		return "M"
	}
	return "?"
}

func changePath(ch *object.Change) string {
	if ch.To.Name != "" {
		return ch.To.Name
	}
	return ch.From.Name
}

func humanRelativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}
