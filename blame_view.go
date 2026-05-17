package main

import (
	"fmt"
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

// blameAuthorWidth is the column width (in characters) for the author column
// in the blame gutter. Names longer than this get ellipsised; the trade-off is
// gutter consistency across rows, which makes the code column line up.
const blameAuthorWidth = 16

// openBlameWindow runs `git blame` against the given tracked file (rel is
// repo-relative, slash-separated) and opens a per-line annotated view. Each
// row shows the commit that last touched that line (short SHA + date +
// author) alongside the line text; clicking a row opens a Historical Diff
// at that commit (file vs its parent), closing the loop with the existing
// history-detail machinery.
//
// Blame is run asynchronously — go-git's Blame walks the commit graph and
// can take several seconds on long-lived files — with a modal progress
// dialog while it computes.
func openBlameWindow(a fyne.App, parent fyne.Window, repo *git.Repository, repoRoot, rel string) {
	if repo == nil || rel == "" {
		return
	}
	head, err := repo.Head()
	if err != nil {
		dialog.ShowError(fmt.Errorf("HEAD: %w", err), parent)
		return
	}
	headCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		dialog.ShowError(fmt.Errorf("HEAD commit: %w", err), parent)
		return
	}

	progLabel := widget.NewLabel(fmt.Sprintf("Computing blame for %s…", rel))
	progBar := widget.NewProgressBarInfinite()
	progDlg := dialog.NewCustom("Blame", "Hide (continues in background)", container.NewVBox(progLabel, progBar), parent)
	progDlg.Show()

	go func() {
		result, blameErr := git.Blame(headCommit, rel)
		fyne.Do(func() {
			progDlg.Hide()
			if blameErr != nil {
				dialog.ShowError(fmt.Errorf("blame %s: %w", rel, blameErr), parent)
				return
			}
			if result == nil || len(result.Lines) == 0 {
				dialog.ShowInformation("Empty blame",
					fmt.Sprintf("git blame returned no lines for %s — the file may be empty in HEAD.", rel),
					parent)
				return
			}
			renderBlameWindow(a, repo, repoRoot, rel, result)
		})
	}()
}

// renderBlameWindow builds the actual blame window from a computed
// BlameResult. Split from openBlameWindow so the progress dialog can run a
// goroutine without holding window construction inside it.
func renderBlameWindow(a fyne.App, repo *git.Repository, repoRoot, rel string, result *git.BlameResult) {
	lines := result.Lines
	lineCount := len(lines)
	lineNoWidth := len(fmt.Sprintf("%d", lineCount))
	if lineNoWidth < 3 {
		lineNoWidth = 3
	}

	rows := make([]string, lineCount)
	for i, ln := range lines {
		rows[i] = formatBlameRow(i+1, lineNoWidth, ln)
	}

	w := a.NewWindow(blameWindowTitle(repoRoot, rel))
	w.SetIcon(resourceKrankyBearNerdPng)

	header := widget.NewLabel(fmt.Sprintf("Blame: %s  •  %d lines  •  Click a row to diff that commit against its parent", rel, lineCount))
	header.TextStyle = fyne.TextStyle{Italic: true}

	list := widget.NewList(
		func() int { return len(rows) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle = fyne.TextStyle{Monospace: true}
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(rows) {
				return
			}
			obj.(*widget.Label).SetText(rows[id])
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		defer list.UnselectAll()
		if id < 0 || id >= len(lines) {
			return
		}
		openBlameLineDiff(a, repo, rel, lines[id].Hash)
	}

	content := container.NewBorder(container.NewVBox(header, widget.NewSeparator()), nil, nil, nil, list)
	w.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(content), w.Canvas()))
	w.Resize(fyne.NewSize(1100, 700))
	w.SetCloseIntercept(func() {
		unregisterRepoChildWindow(w)
		fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
		windowHide(w)
	})
	registerRepoChildWindow(w)
	windowShow(w)
}

// formatBlameRow renders a single blame line as a fixed-width gutter (line
// number + short SHA + date + author) followed by the source text, separated
// by a vertical bar. The fixed widths keep the source column aligned across
// rows even though widget.Label is variable-width by default — Monospace
// font on the label is what actually makes the columns line up.
func formatBlameRow(lineNo, lineNoWidth int, ln *git.Line) string {
	if ln == nil {
		return ""
	}
	sha := ln.Hash.String()
	if len(sha) > 7 {
		sha = sha[:7]
	}
	date := ln.Date.Format("2006-01-02")
	author := ln.AuthorName
	if author == "" {
		author = ln.Author
	}
	author = truncatePad(author, blameAuthorWidth)
	// Strip the trailing newline that go-git keeps on each line so the row
	// doesn't render with a hanging blank in the label.
	text := strings.TrimRight(ln.Text, "\r\n")
	return fmt.Sprintf("%*d  %s  %s  %s │ %s", lineNoWidth, lineNo, sha, date, author, text)
}

// truncatePad fits s into exactly width runes — truncates with an ellipsis
// when too long, pads with spaces when too short. Width is measured in
// runes, not bytes, so multibyte author names don't overflow.
func truncatePad(s string, width int) string {
	r := []rune(s)
	if len(r) > width {
		if width <= 1 {
			return string(r[:width])
		}
		return string(r[:width-1]) + "…"
	}
	if len(r) < width {
		return s + strings.Repeat(" ", width-len(r))
	}
	return s
}

// openBlameLineDiff resolves the commit identified by hash and opens a
// Historical Diff for rel between that commit and its parent. Mirrors the
// (commit vs parent) detail-pane flow in history_view.go's openFileDiff so
// the diff window labels and read-only behaviour match what the user already
// sees when clicking commits in the history window.
func openBlameLineDiff(a fyne.App, repo *git.Repository, rel string, hash plumbing.Hash) {
	commit, err := repo.CommitObject(hash)
	if err != nil {
		dialog.ShowError(fmt.Errorf("resolve %s: %w", shortSHA(hash.String()), err), nil)
		return
	}
	newContent, newErr := readFileAtCommit(commit, rel)
	if newErr != nil {
		dialog.ShowError(fmt.Errorf("read %s at %s: %w", rel, shortSHA(hash.String()), newErr), nil)
		return
	}

	var parent *object.Commit
	if commit.NumParents() > 0 {
		if p, perr := commit.Parent(0); perr == nil {
			parent = p
		}
	}
	var oldContent string
	if parent != nil {
		oldContent, _ = readFileAtCommit(parent, rel) // empty when the file did not exist in parent (e.g. introduced in this commit)
	}

	short := shortSHA(commit.Hash.String())
	commitDate := commit.Author.When.Format("2006-01-02")
	rightLabel := fmt.Sprintf("%s %s: %s", short, commitDate, rel)
	rightSubject := firstLine(commit.Message)

	leftLabel := "(root)"
	leftSubject := ""
	if parent != nil {
		ps := shortSHA(parent.Hash.String())
		leftLabel = fmt.Sprintf("%s %s: %s", ps, parent.Author.When.Format("2006-01-02"), rel)
		leftSubject = firstLine(parent.Message)
	}

	openDiffWindowWithPreload(a, false,
		leftLabel, oldContent, rightLabel, newContent,
		leftSubject, rightSubject,
		true, // leftReadOnly — historical
		true, // rightReadOnly — historical
	)
}

// readFileAtCommit fetches the contents of rel at commit's tree. Returns an
// empty string with nil error when the file simply isn't in that tree
// (e.g. it was added in the next commit), so callers can render a one-sided
// diff without an error popup.
func readFileAtCommit(commit *object.Commit, rel string) (string, error) {
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}
	file, err := tree.File(rel)
	if err != nil {
		// File-not-in-tree is benign for diff purposes; surface only the empty side.
		return "", nil
	}
	return file.Contents()
}

func blameWindowTitle(repoRoot, rel string) string {
	repoName := filepath.Base(repoRoot)
	if repoName == "" || repoName == "." || repoName == "/" {
		repoName = repoRoot
	}
	return fmt.Sprintf("Blame: %s — %s", repoName, rel)
}
