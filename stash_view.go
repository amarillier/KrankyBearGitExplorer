package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// stashEntry is one line of `git stash list`. Date is the raw ISO 8601
// string from git's `%cI` format — kept verbatim for display, no parsing
// because we just show it.
type stashEntry struct {
	Index   int    // 0-based; corresponds to stash@{N}
	Hash    string // SHA of the stash commit (it IS a commit, just under refs/stash)
	Subject string
	Date    string
}

// gatherStashes shells to `git stash list` since go-git doesn't expose a
// public listing API for stashes. Empty list (no stashes) is normal —
// returned as nil with nil error.
func gatherStashes(repoRoot string) ([]stashEntry, error) {
	cmd := exec.Command("git", "-C", repoRoot, "stash", "list", "--format=%H|%s|%cI")
	out, err := cmd.Output()
	if err != nil {
		// `git stash list` with no stashes still exits 0 with empty output;
		// non-zero exit means git itself complained (probably not a repo).
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git stash list: %w\n%s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git stash list: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	var rows []stashEntry
	for i, line := range strings.Split(trimmed, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		rows = append(rows, stashEntry{
			Index:   i,
			Hash:    parts[0],
			Subject: parts[1],
			Date:    parts[2],
		})
	}
	return rows, nil
}

// showStashesDialog lists the repo's stashes. Clicking a row opens the
// per-stash detail dialog (file list → click → Historical Diff vs the
// stash's parent commit).
func showStashesDialog(repo *git.Repository, parent fyne.Window, repoRoot string) {
	if repo == nil {
		return
	}
	rows, err := gatherStashes(repoRoot)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}
	if len(rows) == 0 {
		dialog.ShowInformation("No stashes",
			"This repository has no saved stashes. Use `git stash push` from your terminal to create one.",
			parent)
		return
	}

	headers := []string{"Stash", "Subject", "Date"}
	header := container.NewGridWithColumns(len(headers))
	for _, h := range headers {
		header.Add(widget.NewLabelWithStyle(h, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	}

	list := widget.NewList(
		func() int { return len(rows) },
		func() fyne.CanvasObject {
			cells := make([]fyne.CanvasObject, len(headers))
			for i := range cells {
				lbl := widget.NewLabel("")
				lbl.Truncation = fyne.TextTruncateEllipsis
				cells[i] = lbl
			}
			return container.NewGridWithColumns(len(headers), cells...)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(rows) {
				return
			}
			r := rows[id]
			cells := o.(*fyne.Container).Objects
			cells[0].(*widget.Label).SetText(fmt.Sprintf("stash@{%d}", r.Index))
			cells[1].(*widget.Label).SetText(r.Subject)
			cells[2].(*widget.Label).SetText(r.Date)
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(rows) {
			return
		}
		s := rows[id]
		list.UnselectAll()
		showStashDetailDialog(repo, parent, repoRoot, s)
	}

	footer := widget.NewLabel(fmt.Sprintf("%d stash(es) — click a row to see what's in it.", len(rows)))

	body := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		footer,
		nil, nil,
		list,
	)
	body.Resize(fyne.NewSize(720, 420))

	d := dialog.NewCustom("Stashes — "+filepath.Base(repoRoot), "Close", body, parent)
	d.Resize(fyne.NewSize(760, 480))
	d.Show()
}

// showStashDetailDialog renders the file changes in a single stash and lets
// the user click any file to open a Historical Diff between the stash and
// its base commit (the commit the stash was made on top of). Pure read-only.
func showStashDetailDialog(repo *git.Repository, parent fyne.Window, repoRoot string, s stashEntry) {
	hash := plumbing.NewHash(s.Hash)
	stashCommit, err := repo.CommitObject(hash)
	if err != nil {
		dialog.ShowError(fmt.Errorf("read stash commit %s: %w", s.Hash, err), parent)
		return
	}
	if stashCommit.NumParents() == 0 {
		dialog.ShowError(fmt.Errorf("stash %s has no parent commit — unexpected stash shape", s.Hash), parent)
		return
	}
	baseCommit, err := stashCommit.Parent(0)
	if err != nil {
		dialog.ShowError(fmt.Errorf("read stash parent: %w", err), parent)
		return
	}
	baseTree, err := baseCommit.Tree()
	if err != nil {
		dialog.ShowError(fmt.Errorf("read base tree: %w", err), parent)
		return
	}
	stashTree, err := stashCommit.Tree()
	if err != nil {
		dialog.ShowError(fmt.Errorf("read stash tree: %w", err), parent)
		return
	}
	changes, err := baseTree.Diff(stashTree)
	if err != nil {
		dialog.ShowError(fmt.Errorf("diff stash: %w", err), parent)
		return
	}

	header := widget.NewLabelWithStyle(
		fmt.Sprintf("stash@{%d}  •  %s  •  %s", s.Index, s.Subject, s.Date),
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true},
	)
	header.Wrapping = fyne.TextWrapWord

	list := widget.NewList(
		func() int { return len(changes) },
		func() fyne.CanvasObject {
			lbl := widget.NewLabel("")
			lbl.Truncation = fyne.TextTruncateEllipsis
			return lbl
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(changes) {
				return
			}
			ch := changes[id]
			act, _ := ch.Action()
			o.(*widget.Label).SetText(fmt.Sprintf("[%s]  %s", actionLetter(act), changePath(ch)))
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(changes) {
			return
		}
		openStashFileDiff(repo, baseCommit, stashCommit, changes[id], s)
		list.UnselectAll()
	}

	hint := widget.NewLabel(fmt.Sprintf("%d file(s) in this stash — click any to diff against its base commit.", len(changes)))
	hint.TextStyle = fyne.TextStyle{Italic: true}

	body := container.NewBorder(
		container.NewVBox(header, hint, widget.NewSeparator()),
		nil, nil, nil,
		list,
	)
	body.Resize(fyne.NewSize(720, 440))

	d := dialog.NewCustom(fmt.Sprintf("Stash detail — %s", filepath.Base(repoRoot)), "Close", body, parent)
	d.Resize(fyne.NewSize(760, 500))
	d.Show()
}

// openStashFileDiff is the analog of historyView.openFileDiff for stashes:
// reads the file's blob on both the base-commit and stash-commit sides and
// opens a Historical-Diff style window (both panes read-only).
func openStashFileDiff(repo *git.Repository, base, stash *object.Commit, ch *object.Change, s stashEntry) {
	oldContent, oldErr := readBlobIfText(repo, ch.From.TreeEntry.Hash)
	newContent, newErr := readBlobIfText(repo, ch.To.TreeEntry.Hash)
	if oldErr == errBlobBinary || newErr == errBlobBinary {
		dialog.ShowInformation("Binary file",
			"This file looks binary (contains NUL bytes). Skipping the text diff to avoid garbage output.",
			fyne.CurrentApp().Driver().AllWindows()[0])
		return
	}

	baseShort := shortSHA(base.Hash.String())
	stashShort := shortSHA(stash.Hash.String())
	baseLabel := fmt.Sprintf("%s %s (base)", baseShort, base.Author.When.Format("2006-01-02"))
	stashLabel := fmt.Sprintf("stash@{%d} %s", s.Index, stashShort)

	oldName := ch.From.Name
	newName := ch.To.Name
	if oldName == "" {
		oldName = newName
	}
	if newName == "" {
		newName = oldName
	}

	leftP := fmt.Sprintf("%s: %s", baseLabel, oldName)
	rightP := fmt.Sprintf("%s: %s", stashLabel, newName)
	openDiffWindowWithPreload(
		fyne.CurrentApp(), false,
		leftP, oldContent, rightP, newContent,
		firstLine(base.Message), s.Subject,
		true, // leftReadOnly — base commit's blob is immutable
		true, // rightReadOnly — the stash blob is immutable
	)
}
