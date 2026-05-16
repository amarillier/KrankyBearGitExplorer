package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
)

// explorerRow wraps a folder-list row so it can receive a primary tap
// (delegated to the existing list selection / descend logic) and a secondary
// tap (right-click) which opens the row's context menu. The pattern mirrors
// diffLineRow in diff_gui.go.
type explorerRow struct {
	widget.BaseWidget
	inner fyne.CanvasObject
	view  *explorerView
	rowID widget.ListItemID
}

func newExplorerRow(view *explorerView, inner fyne.CanvasObject) *explorerRow {
	r := &explorerRow{view: view, inner: inner}
	r.ExtendBaseWidget(r)
	return r
}

func (r *explorerRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.inner)
}

func (r *explorerRow) Tapped(_ *fyne.PointEvent) {
	if r.view == nil || r.view.list == nil {
		return
	}
	r.view.list.Select(r.rowID)
}

func (r *explorerRow) TappedSecondary(ev *fyne.PointEvent) {
	if r.view != nil {
		r.view.showRowContextMenu(r.rowID, ev.AbsolutePosition)
	}
}

// showRowContextMenu builds and pops up the per-row context menu. Action items
// are enabled based on whether the row is a file, a directory, .git, tracked,
// untracked, etc. — there's no point offering `git rm` on an untracked file or
// "Diff against HEAD" on something that has never been committed.
func (v *explorerView) showRowContextMenu(id widget.ListItemID, pos fyne.Position) {
	if id < 0 || id >= len(v.entries) || v.win == nil {
		return
	}
	e := v.entries[id]

	// .git is its own affordance (click switches to tracked view); no menu.
	if e.isGit {
		return
	}

	absPath := filepath.Join(v.currentPath, e.name)
	inRepo := v.repo != nil && v.repoRoot != "" && e.rel != ""
	tracked := inRepo && isTrackedStatus(e.status)
	canDiffHEAD := tracked && !e.isDir && !strings.HasPrefix(e.status, "A") && e.status != "??"
	untracked := inRepo && e.status == "??" && !e.isDir
	ignored := inRepo && e.status == "!!" && !e.isDir
	canGitAdd := untracked
	canIgnore := untracked
	canUnignore := ignored
	canDeleteDisk := (untracked || ignored) && !e.isDir

	reveal := fyne.NewMenuItem("Reveal in "+osFileManagerLabel(), func() {
		v.revealInOS(absPath)
	})
	copyPath := fyne.NewMenuItem("Copy path", func() {
		v.copyPath(absPath)
	})
	openEditor := fyne.NewMenuItem("Open in $EDITOR", func() {
		v.openInEditor(absPath)
	})
	openEditor.Disabled = e.isDir

	diffHEAD := fyne.NewMenuItem("Diff against HEAD…", func() {
		v.diffAgainstHEAD(e.rel, absPath)
	})
	diffHEAD.Disabled = !canDiffHEAD

	showHistory := fyne.NewMenuItem("Show history for this file…", func() {
		v.showFileHistory(e.rel)
	})
	// Same enable condition as Diff vs HEAD: the file must be tracked and
	// already committed (an "added (staged)" file has no commits yet).
	showHistory.Disabled = !canDiffHEAD

	gitAdd := fyne.NewMenuItem("git add (start tracking)…", func() {
		v.gitAdd(e.name, e.rel)
	})
	gitAdd.Disabled = !canGitAdd

	addIgnore := fyne.NewMenuItem("Add to .gitignore…", func() {
		v.addToGitignore(e.name, e.rel)
	})
	addIgnore.Disabled = !canIgnore

	unignore := fyne.NewMenuItem("Un-ignore (allow tracking)…", func() {
		v.unignoreFile(e.name, e.rel)
	})
	unignore.Disabled = !canUnignore

	gitRm := fyne.NewMenuItem("git rm -f (remove from worktree + index)…", func() {
		v.gitRmForce(e.name, e.rel, absPath)
	})
	gitRm.Disabled = !tracked || e.isDir

	gitRmCached := fyne.NewMenuItem("git rm --cached (remove from index only)…", func() {
		v.gitRmCached(e.name, e.rel)
	})
	gitRmCached.Disabled = !tracked || e.isDir

	deleteDisk := fyne.NewMenuItem("Delete from disk…", func() {
		v.deleteFromDisk(e.name, absPath)
	})
	deleteDisk.Disabled = !canDeleteDisk

	items := []*fyne.MenuItem{
		reveal,
		copyPath,
		openEditor,
		fyne.NewMenuItemSeparator(),
		diffHEAD,
		showHistory,
		fyne.NewMenuItemSeparator(),
		gitAdd,
		addIgnore,
		unignore,
		fyne.NewMenuItemSeparator(),
		gitRm,
		gitRmCached,
		deleteDisk,
	}
	menu := fyne.NewMenu("", items...)

	if v.contextPop != nil {
		fynetooltip.DestroyPopUpToolTipLayer(v.contextPop)
		v.contextPop = nil
	}
	menuW := widget.NewMenu(menu)
	menuW.Resize(menuW.MinSize())
	pop := widget.NewPopUp(menuW, v.win.Canvas())
	v.contextPop = pop
	fynetooltip.AddPopUpToolTipLayer(pop)
	menuW.OnDismiss = func() {
		fynetooltip.DestroyPopUpToolTipLayer(pop)
		if v.contextPop == pop {
			v.contextPop = nil
		}
		pop.Hide()
	}
	pop.ShowAtPosition(pos)
}

// isTrackedStatus returns true for files git considers tracked (i.e. in the
// index). Untracked ("??") and ignored ("!!") files are excluded.
func isTrackedStatus(raw string) bool {
	return raw != "" && raw != "??" && raw != "!!"
}

func osFileManagerLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return "Finder"
	case "windows":
		return "Explorer"
	default:
		return "file manager"
	}
}

func (v *explorerView) copyPath(path string) {
	c := fyne.CurrentApp().Clipboard()
	if c != nil {
		c.SetContent(path)
	}
}

// revealInOS asks the OS file manager to reveal/select the given path. On
// Linux there is no portable "reveal" verb, so we open the containing folder
// with xdg-open instead — the path itself is still useful via Copy path.
func (v *explorerView) revealInOS(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+path)
	default:
		dir := filepath.Dir(path)
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		dialog.ShowError(fmt.Errorf("reveal: %w", err), v.win)
	}
}

// openInEditor launches $VISUAL (preferred) or $EDITOR with the file. When
// neither env var is set, falls back to the OS' default open command so the
// user still gets something useful.
func (v *explorerView) openInEditor(path string) {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor != "" {
		// Allow $EDITOR to be a command with flags (e.g. "code --wait").
		parts := strings.Fields(editor)
		args := append(parts[1:], path)
		if err := exec.Command(parts[0], args...).Start(); err != nil {
			dialog.ShowError(fmt.Errorf("open in editor: %w", err), v.win)
		}
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		dialog.ShowError(fmt.Errorf("open: %w", err), v.win)
	}
}

// gitRmForce removes the file from both the worktree and the git index, the
// same effect as `git rm -f <file>`. Uses go-git's worktree.Remove.
func (v *explorerView) gitRmForce(displayName, rel, absPath string) {
	if v.repo == nil {
		return
	}
	msg := fmt.Sprintf("Remove %q from the worktree and stage its deletion for the next commit?\n\nThis deletes the file on disk. You'll still need to commit and push the change.", displayName)
	dialog.ShowConfirm("git rm -f", msg, func(ok bool) {
		if !ok {
			return
		}
		wt, err := v.repo.Worktree()
		if err != nil {
			dialog.ShowError(fmt.Errorf("worktree: %w", err), v.win)
			return
		}
		if _, err := wt.Remove(rel); err != nil {
			// Fall back to a plain disk delete if go-git rejects the path (e.g.
			// the file was already removed from the worktree); then try Remove
			// again to update the index.
			_ = os.Remove(absPath)
			if _, err2 := wt.Remove(rel); err2 != nil {
				dialog.ShowError(fmt.Errorf("git rm: %w", err), v.win)
				return
			}
		}
		v.refresh()
	}, v.win)
}

// gitRmCached removes the file from the git index only, leaving the on-disk
// copy intact — the equivalent of `git rm --cached <file>`. go-git does not
// expose a one-liner for this, and shelling out matches CLI semantics
// (respects hooks, config) so we use the system `git` binary here.
func (v *explorerView) gitRmCached(displayName, rel string) {
	if v.repoRoot == "" {
		return
	}
	msg := fmt.Sprintf("Remove %q from the git index (untrack it) but keep the file on disk?\n\nTypical use: you're about to add a matching pattern to .gitignore.", displayName)
	dialog.ShowConfirm("git rm --cached", msg, func(ok bool) {
		if !ok {
			return
		}
		cmd := exec.Command("git", "-C", v.repoRoot, "rm", "--cached", "--", rel)
		out, err := cmd.CombinedOutput()
		if err != nil {
			dialog.ShowError(fmt.Errorf("git rm --cached: %w\n%s", err, strings.TrimSpace(string(out))), v.win)
			return
		}
		v.refresh()
	}, v.win)
}

// gitAdd stages an untracked file via go-git's worktree.Add, the same as
// `git add <path>` on the CLI. After staging the file shows up as
// "added (staged)" in the next refresh.
func (v *explorerView) gitAdd(displayName, rel string) {
	if v.repo == nil {
		return
	}
	msg := fmt.Sprintf("Stage %q for the next commit (git add)?", displayName)
	dialog.ShowConfirm("git add", msg, func(ok bool) {
		if !ok {
			return
		}
		wt, err := v.repo.Worktree()
		if err != nil {
			dialog.ShowError(fmt.Errorf("worktree: %w", err), v.win)
			return
		}
		if _, err := wt.Add(rel); err != nil {
			dialog.ShowError(fmt.Errorf("git add: %w", err), v.win)
			return
		}
		v.refresh()
	}, v.win)
}

// addToGitignore appends an anchored entry for the given repo-relative path
// to the repo's root .gitignore. Anchored (leading slash) so the entry
// matches exactly that path, not every file of that name anywhere in the
// tree. Creates .gitignore if it doesn't exist yet. Leaves a leading
// newline only when the existing file doesn't already end on one — small
// quality-of-life so the appended line lands on its own row.
func (v *explorerView) addToGitignore(displayName, rel string) {
	if v.repoRoot == "" || rel == "" {
		return
	}
	entry := "/" + filepath.ToSlash(rel)
	msg := fmt.Sprintf("Add %q to .gitignore?\n\nWritten as a leading-slash entry (%s) so it matches only this exact path, not every file with the same name elsewhere in the repo.", displayName, entry)
	dialog.ShowConfirm("Add to .gitignore", msg, func(ok bool) {
		if !ok {
			return
		}
		path := filepath.Join(v.repoRoot, ".gitignore")
		existing, _ := os.ReadFile(path)
		var line string
		switch {
		case len(existing) == 0:
			line = entry + "\n"
		case existing[len(existing)-1] == '\n':
			line = entry + "\n"
		default:
			line = "\n" + entry + "\n"
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			dialog.ShowError(fmt.Errorf("open .gitignore: %w", err), v.win)
			return
		}
		defer f.Close()
		if _, err := f.WriteString(line); err != nil {
			dialog.ShowError(fmt.Errorf("write .gitignore: %w", err), v.win)
			return
		}
		v.refresh()
	}, v.win)
}

// unignoreFile flips an ignored file back to visible-to-git. Two paths:
//
//   - If .gitignore has an exact-match line for the file (either anchored
//     "/rel/path" or unanchored "rel/path"), that line is removed. Cleanest
//     undo when the file was added to .gitignore via "Add to .gitignore…"
//     in this same menu.
//   - Otherwise the file is ignored by a broader pattern (e.g. `*.zzz`,
//     `bin/*`, a parent dir's .gitignore). Removing the broader pattern
//     would un-ignore other files too, so instead an anchored negation
//     entry "!/rel/path" is appended. That exempts just this one file
//     without disturbing the existing pattern.
//
// The confirm dialog tells the user which path will be taken so the
// .gitignore edit isn't surprising.
func (v *explorerView) unignoreFile(displayName, rel string) {
	if v.repoRoot == "" || rel == "" {
		return
	}
	slashRel := filepath.ToSlash(rel)
	anchored := "/" + slashRel
	path := filepath.Join(v.repoRoot, ".gitignore")

	existing, _ := os.ReadFile(path) // missing file is fine — empty content

	// Look for an exact-match line first so we know which branch the
	// confirm dialog should describe.
	lines := strings.Split(string(existing), "\n")
	matchIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == anchored || trimmed == slashRel {
			matchIdx = i
			break
		}
	}

	var msg string
	if matchIdx >= 0 {
		msg = fmt.Sprintf("Un-ignore %q?\n\nFound an exact-match line in .gitignore (%q) — it will be removed.", displayName, strings.TrimSpace(lines[matchIdx]))
	} else {
		msg = fmt.Sprintf("Un-ignore %q?\n\nNo exact-match line in .gitignore — the file is ignored by a broader pattern. A negation entry (%q) will be appended to .gitignore, exempting this file without disturbing the broader pattern.", displayName, "!"+anchored)
	}

	dialog.ShowConfirm("Un-ignore", msg, func(ok bool) {
		if !ok {
			return
		}
		var newContent string
		if matchIdx >= 0 {
			kept := append(lines[:matchIdx:matchIdx], lines[matchIdx+1:]...)
			newContent = strings.Join(kept, "\n")
		} else {
			content := string(existing)
			negation := "!" + anchored
			switch {
			case len(content) == 0:
				newContent = negation + "\n"
			case content[len(content)-1] == '\n':
				newContent = content + negation + "\n"
			default:
				newContent = content + "\n" + negation + "\n"
			}
		}
		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			dialog.ShowError(fmt.Errorf("write .gitignore: %w", err), v.win)
			return
		}
		v.refresh()
	}, v.win)
}

// deleteFromDisk removes a file from the working tree via os.Remove. Only
// offered for untracked or ignored files (tracked files have git rm); only
// for files, not directories. Destructive — wording in the confirm makes
// that explicit.
func (v *explorerView) deleteFromDisk(displayName, absPath string) {
	msg := fmt.Sprintf("Delete %q from disk?\n\nThis removes the file from the filesystem. It is not staged in git (the file is untracked or ignored, so there's nothing for git to stage). The deletion cannot be undone from the explorer.", displayName)
	dialog.ShowConfirm("Delete from disk", msg, func(ok bool) {
		if !ok {
			return
		}
		if err := os.Remove(absPath); err != nil {
			dialog.ShowError(fmt.Errorf("delete %s: %w", absPath, err), v.win)
			return
		}
		v.refresh()
	}, v.win)
}

// diffAgainstHEAD opens the existing two-pane diff view with the HEAD blob on
// the left (read-only) and the current worktree file on the right (editable).
// The user can use the existing "Apply left to right" merge action to bring
// HEAD lines back into the worktree; the reverse direction is blocked because
// editing the committed history from here would be a footgun.
func (v *explorerView) diffAgainstHEAD(rel, absPath string) {
	if v.repo == nil {
		return
	}
	head, err := v.repo.Head()
	if err != nil {
		dialog.ShowError(fmt.Errorf("HEAD: %w", err), v.win)
		return
	}
	commit, err := v.repo.CommitObject(head.Hash())
	if err != nil {
		dialog.ShowError(fmt.Errorf("HEAD commit: %w", err), v.win)
		return
	}
	tree, err := commit.Tree()
	if err != nil {
		dialog.ShowError(fmt.Errorf("HEAD tree: %w", err), v.win)
		return
	}
	file, err := tree.File(rel)
	if err != nil {
		dialog.ShowError(fmt.Errorf("file not in HEAD: %s", rel), v.win)
		return
	}
	headContent, err := file.Contents()
	if err != nil {
		dialog.ShowError(fmt.Errorf("read HEAD blob: %w", err), v.win)
		return
	}
	workBytes, err := os.ReadFile(absPath)
	if err != nil {
		dialog.ShowError(fmt.Errorf("read worktree file: %w", err), v.win)
		return
	}

	shortSha := head.Hash().String()
	if len(shortSha) > 7 {
		shortSha = shortSha[:7]
	}
	leftLabel := fmt.Sprintf("HEAD@%s: %s", shortSha, rel)
	openDiffWindowWithPreload(v.app, false,
		leftLabel, headContent, absPath, string(workBytes),
		"", "", // no per-side subtitles for HEAD-vs-worktree
		true,  // leftReadOnly — HEAD side is immutable
		false, // rightReadOnly — worktree side is editable
	)
}
