package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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
	commits        []*object.Commit
	selectedCommit *object.Commit
	changes        object.Changes

	commitList    *widget.List
	fileList      *widget.List
	headerLabel   *widget.Label
	messageLabel  *widget.Label
	hintLabel     *widget.Label
	loadMoreBtn   *ttwidget.Button
	commitCountLb *widget.Label
}

func openHistoryWindow(a fyne.App, repo *git.Repository, repoRoot string, parent fyne.Window) *historyView {
	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
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
	}
	v.loadMoreCommits(historyPageSize)

	w := a.NewWindow(fmt.Sprintf("%s — Repo History: %s", appName, filepath.Base(repoRoot)))
	v.win = w
	w.SetIcon(resourceKrankyBearNerdPng)
	w.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(v.buildUI()), w.Canvas()))
	w.Resize(fyne.NewSize(1000, 700))
	w.SetCloseIntercept(func() {
		if v.commitIter != nil {
			v.commitIter.Close()
			v.commitIter = nil
		}
		fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
		windowHide(w)
	})
	windowShow(w)
	v.refreshHeader()
	return v
}

// loadMoreCommits pulls up to n more commits from the iterator. When the
// iterator is exhausted, it's closed and nilled so the "Load more" button
// can hide itself.
func (v *historyView) loadMoreCommits(n int) {
	if v.commitIter == nil {
		return
	}
	for i := 0; i < n; i++ {
		c, err := v.commitIter.Next()
		if err == io.EOF {
			v.commitIter.Close()
			v.commitIter = nil
			return
		}
		if err != nil {
			v.commitIter.Close()
			v.commitIter = nil
			return
		}
		v.commits = append(v.commits, c)
	}
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
			return container.NewVBox(subject, meta)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(v.commits) {
				return
			}
			c := v.commits[id]
			box := o.(*fyne.Container)
			subject := box.Objects[0].(*widget.Label)
			meta := box.Objects[1].(*widget.Label)
			subject.SetText(firstLine(c.Message))
			short := c.Hash.String()
			if len(short) > 7 {
				short = short[:7]
			}
			meta.SetText(fmt.Sprintf("%s  •  %s  •  %s", short, c.Author.Name, humanRelativeTime(c.Author.When)))
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
		v.commitList.Refresh()
		v.refreshHeader()
	})
	v.loadMoreBtn.SetToolTip(fmt.Sprintf("Load the next %d commits", historyPageSize))
	v.loadMoreBtn.Importance = widget.LowImportance

	leftFooter := container.NewBorder(nil, nil, v.commitCountLb, v.loadMoreBtn, nil)
	leftPane := container.NewBorder(nil, leftFooter, nil, nil, v.commitList)

	// --- right pane: commit detail + file list ----------------------------
	v.headerLabel = widget.NewLabel("(select a commit)")
	v.headerLabel.TextStyle = fyne.TextStyle{Bold: true}
	v.headerLabel.Truncation = fyne.TextTruncateEllipsis

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

	detailHeader := container.NewVBox(
		v.headerLabel,
		v.messageLabel,
		v.hintLabel,
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
	more := ""
	if v.commitIter == nil {
		more = " (all loaded)"
	}
	v.commitCountLb.SetText(fmt.Sprintf("%d commits loaded%s", len(v.commits), more))
	if v.loadMoreBtn != nil {
		if v.commitIter == nil {
			v.loadMoreBtn.Disable()
		} else {
			v.loadMoreBtn.Enable()
		}
	}
}

func (v *historyView) selectCommit(c *object.Commit) {
	v.selectedCommit = c
	short := c.Hash.String()
	if len(short) > 7 {
		short = short[:7]
	}
	v.headerLabel.SetText(fmt.Sprintf("%s  •  %s <%s>  •  %s",
		short, c.Author.Name, c.Author.Email,
		c.Author.When.Format("2006-01-02 15:04 MST"),
	))
	v.messageLabel.SetText(strings.TrimSpace(c.Message))

	v.changes = nil
	if c.NumParents() == 0 {
		// Initial commit — synthesise an "all files added" change list by
		// walking the commit's tree.
		if tree, err := c.Tree(); err == nil {
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
		v.hintLabel.SetText(fmt.Sprintf("%d file(s) in initial commit — click any to view its contents.", len(v.changes)))
	} else {
		parent, err := c.Parent(0)
		if err == nil && parent != nil {
			pt, perr := parent.Tree()
			ct, cerr := c.Tree()
			if perr == nil && cerr == nil {
				changes, derr := pt.Diff(ct)
				if derr == nil {
					v.changes = changes
				}
			}
		}
		v.hintLabel.SetText(fmt.Sprintf("%d file(s) changed vs parent — click any to open the diff.", len(v.changes)))
	}
	v.fileList.Refresh()
	v.fileList.UnselectAll()
}

func (v *historyView) openFileDiff(ch *object.Change) {
	if v.repo == nil || v.selectedCommit == nil {
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

	short := v.selectedCommit.Hash.String()
	if len(short) > 7 {
		short = short[:7]
	}
	commitDate := v.selectedCommit.Author.When.Format("2006-01-02")
	commitLabel := fmt.Sprintf("%s %s", short, commitDate)
	commitSubject := firstLine(v.selectedCommit.Message)

	// Parent label + subject: SHA + date so two historical diffs opened
	// side-by-side are distinguishable even when they share a file path.
	// "(root)" stands in for the synthetic pre-history when the selected
	// commit is initial; in that case the left subtitle stays empty (there's
	// no commit to caption).
	parentLabel := "(root)"
	parentSubject := ""
	if v.selectedCommit.NumParents() > 0 {
		if parent, err := v.selectedCommit.Parent(0); err == nil {
			ps := parent.Hash.String()
			if len(ps) > 7 {
				ps = ps[:7]
			}
			parentLabel = fmt.Sprintf("%s %s", ps, parent.Author.When.Format("2006-01-02"))
			parentSubject = firstLine(parent.Message)
		}
	}

	oldName := ch.From.Name
	newName := ch.To.Name
	if oldName == "" {
		oldName = newName
	}
	if newName == "" {
		newName = oldName
	}

	leftP := fmt.Sprintf("%s: %s", parentLabel, oldName)
	rightP := fmt.Sprintf("%s: %s", commitLabel, newName)
	openDiffWindowWithPreload(v.app, false,
		leftP, oldContent, rightP, newContent,
		parentSubject, commitSubject,
		true, // leftReadOnly — parent blob is immutable
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
