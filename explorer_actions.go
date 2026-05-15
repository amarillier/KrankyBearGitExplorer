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

	gitRm := fyne.NewMenuItem("git rm -f (remove from worktree + index)…", func() {
		v.gitRmForce(e.name, e.rel, absPath)
	})
	gitRm.Disabled = !tracked || e.isDir

	gitRmCached := fyne.NewMenuItem("git rm --cached (remove from index only)…", func() {
		v.gitRmCached(e.name, e.rel)
	})
	gitRmCached.Disabled = !tracked || e.isDir

	items := []*fyne.MenuItem{
		reveal,
		copyPath,
		openEditor,
		fyne.NewMenuItemSeparator(),
		diffHEAD,
		fyne.NewMenuItemSeparator(),
		gitRm,
		gitRmCached,
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
	openDiffWindowWithPreload(v.app, false, leftLabel, headContent, absPath, string(workBytes), true /* leftReadOnly */)
}
