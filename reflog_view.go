package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// reflogEntry is one line from `git reflog` parsed into its constituent
// parts. `index` is computed from the row's position in the list, since
// git's reflog output is always newest-first (newest = HEAD@{0}).
type reflogEntry struct {
	index   int    // position in the reflog; HEAD@{index}
	hash    plumbing.Hash
	date    string // "YYYY-MM-DD HH:MM" parsed out of %gd with --date=iso
	action  string // subject prefix before the first ":" (commit, checkout, reset, ...)
	message string // subject after the first ":" (may be empty for actions without one)
}

// gatherReflog runs `git -C <repo> reflog --date=iso ...` and parses each
// line into a reflogEntry. NULL bytes between fields keep the parsing
// robust against ":" or "|" appearing inside a reflog subject.
//
// Returns nil with the error so the caller can show a meaningful dialog
// — vs the silent fallback in gatherCommitSignatures which is more
// decorative. A working reflog viewer is the entire point of this view,
// so silent fallback would be confusing.
func gatherReflog(repoRoot string) ([]reflogEntry, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("no repository root")
	}
	cmd := exec.Command("git", "-C", repoRoot, "reflog", "--date=iso", "--format=%H%x00%gd%x00%gs")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git reflog: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	var entries []reflogEntry
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) < 3 {
			continue
		}
		hash := plumbing.NewHash(parts[0])
		if hash.IsZero() {
			continue
		}
		entries = append(entries, reflogEntry{
			index:   len(entries),
			hash:    hash,
			date:    parseReflogDate(parts[1]),
			action:  splitReflogAction(parts[2]),
			message: splitReflogMessage(parts[2]),
		})
	}
	return entries, nil
}

// parseReflogDate pulls the "YYYY-MM-DD HH:MM" prefix from %gd output of
// the form "HEAD@{2026-05-19 14:23:15 -0500}". Returns "" if the input
// doesn't look like a reflog selector (an unfamiliar git version, say),
// so the UI just shows an empty date column instead of garbage.
func parseReflogDate(selector string) string {
	open := strings.Index(selector, "{")
	close := strings.LastIndex(selector, "}")
	if open < 0 || close < 0 || close <= open {
		return ""
	}
	inner := selector[open+1 : close]
	// Inner is "2026-05-19 14:23:15 -0500" — keep date + HH:MM, drop seconds + tz.
	if len(inner) >= 16 {
		return inner[:16]
	}
	return inner
}

// splitReflogAction returns the leading action keyword from a reflog
// subject like "commit: ...", "checkout: moving from main to feature",
// "reset: moving to HEAD~3". Subjects without a colon (rare — "branch"
// entries on older git versions) fall back to the whole string.
func splitReflogAction(subject string) string {
	colon := strings.Index(subject, ":")
	if colon < 0 {
		return strings.TrimSpace(subject)
	}
	return strings.TrimSpace(subject[:colon])
}

func splitReflogMessage(subject string) string {
	colon := strings.Index(subject, ":")
	if colon < 0 {
		return ""
	}
	return strings.TrimSpace(subject[colon+1:])
}

// reflogView is the secondary window: a list of reflog entries on the
// left, a per-entry commit detail pane on the right. Clicking a file
// in the right pane opens a Historical Diff between that commit's blob
// and its parent's blob.
type reflogView struct {
	app      fyne.App
	win      fyne.Window
	repo     *git.Repository
	repoRoot string

	entries  []reflogEntry
	selected *object.Commit
	changes  object.Changes

	entryList    *widget.List
	headerLabel  *widget.Label
	actionLabel  *widget.Label
	messageLabel *widget.Label
	hintLabel    *widget.Label
	fileList     *widget.List
}

// openReflogWindow shells out for the reflog, opens the master/detail
// view, and registers the window as a repo-bound child so the cross-repo
// switch prompt can offer to close it.
func openReflogWindow(a fyne.App, parent fyne.Window, repo *git.Repository, repoRoot string) {
	if repo == nil || repoRoot == "" {
		return
	}
	entries, err := gatherReflog(repoRoot)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}
	if len(entries) == 0 {
		dialog.ShowInformation("Empty reflog",
			"This repository's reflog is empty — typical for a freshly-cloned or freshly-init'd repo. The reflog grows as HEAD moves (commit, checkout, reset, rebase, ...).",
			parent)
		return
	}

	v := &reflogView{
		app:      a,
		repo:     repo,
		repoRoot: repoRoot,
		entries:  entries,
	}
	w := a.NewWindow(reflogWindowTitle(repoRoot))
	v.win = w
	w.SetIcon(resourceKrankyBearNerdPng)
	w.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(v.buildUI()), w.Canvas()))
	w.Resize(fyne.NewSize(1100, 700))
	w.SetCloseIntercept(func() {
		unregisterRepoChildWindow(w)
		fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
		windowHide(w)
	})
	registerRepoChildWindow(w)
	windowShow(w)
}

func reflogWindowTitle(repoRoot string) string {
	repoName := filepath.Base(repoRoot)
	if repoName == "" || repoName == "." || repoName == "/" {
		repoName = repoRoot
	}
	return "Reflog: " + repoName
}

// reflogRowWidth constants match the gist of refs_view's: a few narrow
// columns of fixed width then the wide message column flexes. Selector
// fits "HEAD@{9999}", action fits the longest common git verb
// ("rebase (continue)") with a little air.
const (
	reflogSelectorWidth = float32(110)
	reflogDateWidth     = float32(130)
	reflogActionWidth   = float32(130)
)

func (v *reflogView) buildUI() fyne.CanvasObject {
	// --- left pane: reflog list -------------------------------------------
	header := container.NewBorder(nil, nil,
		container.NewHBox(
			container.NewMax(sizingRect(reflogSelectorWidth), boldLabel("Selector")),
			container.NewMax(sizingRect(reflogDateWidth), boldLabel("Date")),
			container.NewMax(sizingRect(reflogActionWidth), boldLabel("Action")),
		),
		nil,
		boldLabel("Message"),
	)

	v.entryList = widget.NewList(
		func() int { return len(v.entries) },
		func() fyne.CanvasObject {
			return newReflogRowWidget()
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(v.entries) {
				return
			}
			e := v.entries[id]
			row := o.(*reflogRowWidget)
			row.selector.SetText(fmt.Sprintf("HEAD@{%d}", e.index))
			row.date.SetText(e.date)
			row.action.SetText(e.action)
			row.message.SetText(e.message)
		},
	)
	v.entryList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(v.entries) {
			return
		}
		v.selectEntry(v.entries[id])
	}

	leftPane := container.NewBorder(header, nil, nil, nil, v.entryList)

	// --- right pane: commit detail + file list ----------------------------
	v.headerLabel = widget.NewLabel("(select a reflog entry)")
	v.headerLabel.TextStyle = fyne.TextStyle{Bold: true}
	v.headerLabel.Truncation = fyne.TextTruncateEllipsis

	v.actionLabel = widget.NewLabel("")
	v.actionLabel.TextStyle = fyne.TextStyle{Italic: true}
	v.actionLabel.Truncation = fyne.TextTruncateEllipsis

	v.messageLabel = widget.NewLabel("")
	v.messageLabel.Wrapping = fyne.TextWrapWord

	v.hintLabel = widget.NewLabel("")
	v.hintLabel.TextStyle = fyne.TextStyle{Italic: true}

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
			lbl.SetText(fmt.Sprintf("[%s]  %s", actionLetter(act), changePath(ch)))
		},
	)
	v.fileList.OnSelected = func(id widget.ListItemID) {
		defer v.fileList.UnselectAll()
		if id < 0 || id >= len(v.changes) {
			return
		}
		v.openFileDiff(v.changes[id])
	}

	detailHeader := container.NewVBox(
		v.headerLabel,
		v.actionLabel,
		v.messageLabel,
		v.hintLabel,
		widget.NewSeparator(),
	)
	rightPane := container.NewBorder(detailHeader, nil, nil, nil, v.fileList)

	split := container.NewHSplit(leftPane, rightPane)
	split.SetOffset(0.5)
	return split
}

// selectEntry resolves the entry's commit object and populates the right
// pane. The commit will exist in the object DB even if it's unreachable
// from current HEAD — the reflog itself keeps it alive — so this works
// for "lost" commits the user might want to recover via the CLI.
func (v *reflogView) selectEntry(e reflogEntry) {
	commit, err := v.repo.CommitObject(e.hash)
	if err != nil {
		v.selected = nil
		v.changes = nil
		v.headerLabel.SetText(fmt.Sprintf("HEAD@{%d}  •  %s", e.index, shortSHA(e.hash.String())))
		v.actionLabel.SetText(strings.TrimSpace(e.action + ": " + e.message))
		v.messageLabel.SetText("(commit object not in this repo's database — possibly a foreign-ref reflog or a pruned object)")
		v.hintLabel.SetText("")
		v.fileList.Refresh()
		v.fileList.UnselectAll()
		return
	}
	v.selected = commit

	short := shortSHA(commit.Hash.String())
	v.headerLabel.SetText(fmt.Sprintf("HEAD@{%d}  •  %s  •  %s <%s>  •  %s",
		e.index, short, commit.Author.Name, commit.Author.Email,
		commit.Author.When.Format("2006-01-02 15:04 MST"),
	))
	actionLine := e.action
	if e.message != "" {
		actionLine = e.action + ": " + e.message
	}
	v.actionLabel.SetText("Reflog action — " + actionLine)
	v.messageLabel.SetText(strings.TrimSpace(commit.Message))

	v.changes = nil
	if commit.NumParents() == 0 {
		// Initial commit — synthesise an "all files added" change list,
		// matching history_view's behaviour for the same case.
		if tree, err := commit.Tree(); err == nil {
			_ = tree.Files().ForEach(func(f *object.File) error {
				v.changes = append(v.changes, &object.Change{
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
		v.hintLabel.SetText(fmt.Sprintf("%d file(s) in this commit — click any to view its contents.", len(v.changes)))
	} else {
		parent, perr := commit.Parent(0)
		if perr == nil && parent != nil {
			pt, pErr := parent.Tree()
			ct, cErr := commit.Tree()
			if pErr == nil && cErr == nil {
				if changes, derr := pt.Diff(ct); derr == nil {
					v.changes = changes
				}
			}
		}
		v.hintLabel.SetText(fmt.Sprintf("%d file(s) changed vs parent — click any to open the diff.", len(v.changes)))
	}
	v.fileList.Refresh()
	v.fileList.UnselectAll()
}

// openFileDiff renders a both-sides-read-only historical diff for the
// given change. Mirrors history_view's openFileDiff but kept inline here
// since reflog has no compare-base concept to thread through.
func (v *reflogView) openFileDiff(ch *object.Change) {
	if v.repo == nil || v.selected == nil {
		return
	}
	oldContent, oldErr := readBlobIfText(v.repo, ch.From.TreeEntry.Hash)
	newContent, newErr := readBlobIfText(v.repo, ch.To.TreeEntry.Hash)
	if oldErr == errBlobBinary || newErr == errBlobBinary {
		dialog.ShowInformation("Binary file",
			"This file looks binary (contains NUL bytes). Skipping the text diff to avoid garbage output.",
			v.win)
		return
	}

	short := shortSHA(v.selected.Hash.String())
	commitDate := v.selected.Author.When.Format("2006-01-02")
	commitLabel := fmt.Sprintf("%s %s", short, commitDate)
	commitSubject := firstLine(v.selected.Message)

	var leftCommit *object.Commit
	if v.selected.NumParents() > 0 {
		if p, err := v.selected.Parent(0); err == nil {
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
		true, // leftReadOnly — historical
		true, // rightReadOnly — historical
	)
}

// reflogRowWidget mirrors refs_view.go's refRowWidget pattern: typed
// references to the labels so the update closure doesn't depend on
// container.Objects ordering, which Fyne shifts between releases.
type reflogRowWidget struct {
	widget.BaseWidget
	selector *widget.Label
	date     *widget.Label
	action   *widget.Label
	message  *widget.Label
}

func newReflogRowWidget() *reflogRowWidget {
	mk := func() *widget.Label {
		l := widget.NewLabel("")
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	mkMono := func() *widget.Label {
		l := widget.NewLabel("")
		l.TextStyle = fyne.TextStyle{Monospace: true}
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	r := &reflogRowWidget{
		selector: mkMono(),
		date:     mk(),
		action:   mk(),
		message:  mk(),
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *reflogRowWidget) CreateRenderer() fyne.WidgetRenderer {
	content := container.NewBorder(nil, nil,
		container.NewHBox(
			container.NewMax(sizingRect(reflogSelectorWidth), r.selector),
			container.NewMax(sizingRect(reflogDateWidth), r.date),
			container.NewMax(sizingRect(reflogActionWidth), r.action),
		),
		nil,
		r.message,
	)
	return widget.NewSimpleRenderer(content)
}

// boldLabel is a one-line helper for the header row's column titles —
// just a label with bold styling that matches the rest of the dialog
// chrome (see refs_view's headers for the convention).
func boldLabel(text string) *widget.Label {
	l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return l
}
