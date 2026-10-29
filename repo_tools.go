package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// gitOut runs git in dir and returns trimmed stdout. Prompts are disabled so
// a repo that wants credentials fails fast instead of hanging a worker.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return strings.TrimSpace(string(out)), fmt.Errorf("%s", msg)
		}
		return strings.TrimSpace(string(out)), err
	}
	return strings.TrimSpace(string(out)), nil
}

// ─────────────────────────────────────────────────────────────────────────
// Quick-switch repo picker (Ctrl/Cmd+P)
// ─────────────────────────────────────────────────────────────────────────

// fuzzyMatch reports whether every rune of query appears in s in order
// (case-insensitive) and a score where lower is better: matches in the base
// name beat matches only in the parent path, and tighter runs beat spread out ones.
func fuzzyMatch(query, path string) (bool, int) {
	if query == "" {
		return true, 0
	}
	q := []rune(strings.ToLower(query))
	base := strings.ToLower(filepath.Base(path))
	if strings.Contains(base, string(q)) {
		return true, strings.Index(base, string(q))
	}
	full := []rune(strings.ToLower(path))
	qi, first, last := 0, -1, 0
	for i, r := range full {
		if qi < len(q) && r == q[qi] {
			if first < 0 {
				first = i
			}
			last = i
			qi++
		}
	}
	if qi < len(q) {
		return false, 0
	}
	return true, 1000 + (last - first)
}

// showQuickSwitch opens a filter-as-you-type list of recent folders plus the
// repos discovered under the configured scan roots. Enter opens the top
// match; clicking a row opens that one.
func (v *explorerView) showQuickSwitch() {
	var mu sync.Mutex
	var candidates []string
	seen := map[string]bool{}
	add := func(paths []string) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range paths {
			p = filepath.Clean(p)
			if !seen[p] {
				seen[p] = true
				candidates = append(candidates, p)
			}
		}
	}
	add(loadRecentFolders(v.app)) // recents first so they win ties and show instantly

	var matches []string
	var list *widget.List
	var d dialog.Dialog
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Type to filter recent folders and repos…")

	refilter := func() {
		mu.Lock()
		type scored struct {
			path  string
			score int
			idx   int
		}
		var hits []scored
		for i, c := range candidates {
			if ok, sc := fuzzyMatch(entry.Text, c); ok {
				hits = append(hits, scored{c, sc, i})
			}
		}
		mu.Unlock()
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })
		matches = matches[:0]
		for _, h := range hits {
			matches = append(matches, h.path)
		}
		if list != nil {
			list.Refresh()
		}
	}

	open := func(path string) {
		if d != nil {
			d.Hide()
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			removeRecentFolder(v.app, path)
			dialog.ShowError(fmt.Errorf("folder no longer exists: %s", path), v.win)
			return
		}
		addRecentFolder(v.app, path)
		v.loadFolder(path)
		v.refreshMenu()
	}

	list = widget.NewList(
		func() int { return len(matches) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("name")
			name.TextStyle = fyne.TextStyle{Bold: true}
			dir := widget.NewLabel("dir")
			dir.TextStyle = fyne.TextStyle{Italic: true}
			dir.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, name, nil, dir)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(matches) {
				return
			}
			row := o.(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(filepath.Base(matches[i]))
			row.Objects[1].(*widget.Label).SetText(filepath.Dir(matches[i]))
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		if i < len(matches) {
			open(matches[i])
		}
	}
	entry.OnChanged = func(string) { refilter() }
	entry.OnSubmitted = func(string) {
		if len(matches) > 0 {
			open(matches[0])
		}
	}
	refilter()

	// Repo discovery walks the filesystem — keep it off the UI thread and
	// merge the results in when ready.
	go func() {
		found := discoverRepos(loadDepScanRoots(v.app), depScanMaxDepth)
		add(found)
		fyne.Do(refilter)
	}()

	body := container.NewBorder(entry, nil, nil, nil, list)
	body.Resize(fyne.NewSize(620, 420))
	d = dialog.NewCustom("Quick switch", "Cancel", body, v.win)
	d.Resize(fyne.NewSize(660, 500))
	d.Show()
	v.win.Canvas().Focus(entry)
}

// ─────────────────────────────────────────────────────────────────────────
// Sync all repos: fetch everywhere, fast-forward the safe ones
// ─────────────────────────────────────────────────────────────────────────

type syncRow struct {
	path       string
	branch     string
	ahead      int
	behind     int
	dirty      bool
	upstream   bool
	status     string
	nameLabel  *widget.Label
	branchLbl  *widget.Label
	aheadLbl   *widget.Label
	statusLbl  *widget.Label
	mu         sync.Mutex
	inspectErr error
}

// inspect fills in branch / ahead / behind / dirty from local state (so run
// it after a fetch for fresh remote info).
func (r *syncRow) inspect(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.branch, r.ahead, r.behind, r.upstream, r.dirty = "", 0, 0, false, false
	br, err := gitOut(ctx, r.path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		r.inspectErr = err
		return
	}
	r.branch = br
	if out, err := gitOut(ctx, r.path, "rev-list", "--left-right", "--count", "HEAD...@{u}"); err == nil {
		if f := strings.Fields(out); len(f) == 2 {
			r.ahead, _ = strconv.Atoi(f[0])
			r.behind, _ = strconv.Atoi(f[1])
			r.upstream = true
		}
	}
	if out, err := gitOut(ctx, r.path, "status", "--porcelain"); err == nil {
		r.dirty = out != ""
	}
}

func (r *syncRow) summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var parts []string
	switch {
	case r.inspectErr != nil:
		return "✗ " + r.inspectErr.Error()
	case !r.upstream:
		parts = append(parts, "no upstream")
	case r.ahead == 0 && r.behind == 0:
		parts = append(parts, "up to date")
	case r.ahead > 0 && r.behind > 0:
		parts = append(parts, "diverged")
	case r.behind > 0:
		parts = append(parts, "behind")
	default:
		parts = append(parts, "ahead (unpushed)")
	}
	if r.dirty {
		parts = append(parts, "dirty")
	}
	return strings.Join(parts, ", ")
}

func (r *syncRow) canFastForward() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.upstream && r.behind > 0 && r.ahead == 0 && !r.dirty
}

// showSyncAllDialog fetches (with --prune) every repo in the configured scan
// roots, shows where each stands, and can fast-forward the ones that are
// behind with nothing local to lose. It never merges, rebases or touches a
// dirty or diverged repo.
func showSyncAllDialog(a fyne.App, parent fyne.Window, onDone func()) {
	repos := effectiveScanRepos(a)
	if len(repos) == 0 {
		dialog.ShowInformation("No repos to sync",
			"No repositories were found. Add source folders under Scan → Configure local folders… and they will be used here too.",
			parent)
		return
	}

	rows := make([]*syncRow, len(repos))
	col := container.NewVBox()
	mono := fyne.TextStyle{Monospace: true}
	for i, p := range repos {
		r := &syncRow{
			path:      p,
			nameLabel: widget.NewLabel(filepath.Base(p)),
			branchLbl: widget.NewLabel(""),
			aheadLbl:  widget.NewLabel(""),
			statusLbl: widget.NewLabel("waiting…"),
		}
		r.nameLabel.TextStyle = fyne.TextStyle{Bold: true}
		r.branchLbl.TextStyle = mono
		r.aheadLbl.TextStyle = mono
		rows[i] = r
		col.Add(container.NewGridWithColumns(4, r.nameLabel, r.branchLbl, r.aheadLbl, r.statusLbl))
	}
	header := container.NewGridWithColumns(4,
		widget.NewLabelWithStyle("Repo", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Branch", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("↓ behind  ↑ ahead", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Status", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)
	scroll := container.NewVScroll(col)
	scroll.SetMinSize(fyne.NewSize(700, 300))

	progress := widget.NewLabel("")
	fetchBtn := widget.NewButtonWithIcon("Fetch all", theme.DownloadIcon(), nil)
	ffBtn := widget.NewButtonWithIcon("Fast-forward behind repos", theme.NavigateNextIcon(), nil)
	ffBtn.Disable()
	var d *dialog.CustomDialog
	closeBtn := widget.NewButton("Close", func() { d.Hide() })

	paint := func(r *syncRow) {
		r.mu.Lock()
		br, ahead, behind, up := r.branch, r.ahead, r.behind, r.upstream
		r.mu.Unlock()
		r.branchLbl.SetText(br)
		if up {
			r.aheadLbl.SetText(fmt.Sprintf("↓%d  ↑%d", behind, ahead))
		} else {
			r.aheadLbl.SetText("—")
		}
		r.statusLbl.SetText(r.summary())
	}
	refreshFF := func() {
		n := 0
		for _, r := range rows {
			if r.canFastForward() {
				n++
			}
		}
		ffBtn.SetText(fmt.Sprintf("Fast-forward behind repos (%d)", n))
		if n > 0 {
			ffBtn.Enable()
		} else {
			ffBtn.Disable()
		}
	}

	// runPool runs fn over every row with limited parallelism, then calls
	// done on the UI thread.
	runPool := func(label string, fn func(*syncRow), done func()) {
		fetchBtn.Disable()
		ffBtn.Disable()
		go func() {
			sem := make(chan struct{}, 4)
			var wg sync.WaitGroup
			var finished int
			var fmu sync.Mutex
			for _, r := range rows {
				wg.Add(1)
				sem <- struct{}{}
				go func(r *syncRow) {
					defer wg.Done()
					defer func() { <-sem }()
					fn(r)
					fmu.Lock()
					finished++
					n := finished
					fmu.Unlock()
					fyne.Do(func() {
						paint(r)
						progress.SetText(fmt.Sprintf("%s %d / %d", label, n, len(rows)))
					})
				}(r)
			}
			wg.Wait()
			fyne.Do(func() {
				fetchBtn.Enable()
				done()
			})
		}()
	}

	fetchBtn.OnTapped = func() {
		for _, r := range rows {
			r.statusLbl.SetText("fetching…")
		}
		runPool("Fetched", func(r *syncRow) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			r.mu.Lock()
			r.inspectErr = nil
			r.mu.Unlock()
			if _, err := gitOut(ctx, r.path, "fetch", "--prune", "--quiet"); err != nil {
				r.inspect(ctx)
				r.mu.Lock()
				r.inspectErr = fmt.Errorf("fetch failed: %w", err)
				r.mu.Unlock()
				return
			}
			r.inspect(ctx)
		}, func() {
			progress.SetText("Fetch complete.")
			refreshFF()
		})
	}

	ffBtn.OnTapped = func() {
		var todo int
		for _, r := range rows {
			if r.canFastForward() {
				todo++
			}
		}
		dialog.ShowConfirm("Fast-forward repos",
			fmt.Sprintf("Fast-forward %d repo(s) to their upstream with git merge --ff-only?\n\nOnly clean repos with no local commits are touched.", todo),
			func(ok bool) {
				if !ok {
					return
				}
				runPool("Updated", func(r *syncRow) {
					if !r.canFastForward() {
						return
					}
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					_, err := gitOut(ctx, r.path, "merge", "--ff-only", "@{u}")
					r.inspect(ctx)
					if err != nil {
						r.mu.Lock()
						r.inspectErr = fmt.Errorf("fast-forward failed: %w", err)
						r.mu.Unlock()
					}
				}, func() {
					progress.SetText("Fast-forward complete.")
					refreshFF()
					if onDone != nil {
						onDone()
					}
				})
			}, parent)
	}

	// Show the current local state straight away; Fetch refreshes it.
	runPool("Checked", func(r *syncRow) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.inspect(ctx)
	}, func() {
		progress.SetText("Local state shown. Fetch all to refresh from the remotes.")
		refreshFF()
	})

	root := container.NewBorder(header, container.NewVBox(widget.NewSeparator(), progress,
		container.NewHBox(fetchBtn, ffBtn, layout.NewSpacer(), closeBtn)), nil, nil, scroll)
	d = dialog.NewCustomWithoutButtons(fmt.Sprintf("Sync all repos — %d found", len(repos)), root, parent)
	d.Resize(fyne.NewSize(900, 560))
	d.Show()
}

// ─────────────────────────────────────────────────────────────────────────
// Stale branch cleanup
// ─────────────────────────────────────────────────────────────────────────

type staleBranch struct {
	name   string
	reason string
}

// defaultBaseBranch is what "merged" is measured against: origin's HEAD when
// known, else main, else master, else the current branch.
func defaultBaseBranch(ctx context.Context, repoRoot, current string) string {
	if ref, err := gitOut(ctx, repoRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(ref, "origin/"); b != "" {
			if _, err := gitOut(ctx, repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
				return b
			}
		}
	}
	for _, b := range []string{"main", "master"} {
		if _, err := gitOut(ctx, repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return current
}

// findStaleBranches lists local branches that are fully merged into base, or
// whose upstream branch has been deleted on the remote. The current branch
// and base are never listed.
func findStaleBranches(ctx context.Context, repoRoot string) (base string, stale []staleBranch, err error) {
	current, err := gitOut(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", nil, err
	}
	base = defaultBaseBranch(ctx, repoRoot, current)
	reasons := map[string]string{}
	var order []string
	note := func(name, reason string) {
		if name == "" || name == current || name == base {
			return
		}
		if old, ok := reasons[name]; ok {
			reasons[name] = old + " + " + reason
			return
		}
		reasons[name] = reason
		order = append(order, name)
	}
	merged, err := gitOut(ctx, repoRoot, "branch", "--merged", base, "--format=%(refname:short)")
	if err != nil {
		return base, nil, err
	}
	for _, l := range strings.Split(merged, "\n") {
		note(strings.TrimSpace(l), "merged into "+base)
	}
	refs, err := gitOut(ctx, repoRoot, "for-each-ref", "refs/heads", "--format=%(refname:short)\t%(upstream:track)")
	if err == nil {
		for _, l := range strings.Split(refs, "\n") {
			name, track, _ := strings.Cut(l, "\t")
			if strings.Contains(track, "gone") {
				note(strings.TrimSpace(name), "upstream deleted")
			}
		}
	}
	sort.Strings(order)
	for _, n := range order {
		stale = append(stale, staleBranch{name: n, reason: reasons[n]})
	}
	return base, stale, nil
}

// showStaleBranchesDialog lists merged / upstream-gone local branches with
// checkboxes and deletes the ticked ones with `git branch -d`, which refuses
// anything not fully merged — so an "upstream deleted" branch with unmerged
// work is reported, never force-removed.
func showStaleBranchesDialog(parent fyne.Window, repoRoot string, onChanged func()) {
	list := container.NewVBox()
	intro := widget.NewLabel("")
	intro.Wrapping = fyne.TextWrapWord
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	var checks []*widget.Check
	var names []string
	deleteBtn := widget.NewButtonWithIcon("Delete selected", theme.DeleteIcon(), nil)
	deleteBtn.Importance = widget.DangerImportance
	pruneBtn := widget.NewButtonWithIcon("Fetch --prune & rescan", theme.ViewRefreshIcon(), nil)
	var d *dialog.CustomDialog
	closeBtn := widget.NewButton("Close", func() { d.Hide() })

	var rescan func()
	rescan = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		base, stale, err := findStaleBranches(ctx, repoRoot)
		list.RemoveAll()
		checks, names = nil, nil
		if err != nil {
			intro.SetText("Couldn't list branches: " + err.Error())
			deleteBtn.Disable()
			return
		}
		if len(stale) == 0 {
			intro.SetText(fmt.Sprintf("Nothing to clean up — no local branches other than %s are merged or orphaned.", base))
			deleteBtn.Disable()
			return
		}
		intro.SetText(fmt.Sprintf("%d branch(es) look safe to delete (merged into %s, or their remote branch is gone). Deletion uses git branch -d, which refuses anything with unmerged commits.", len(stale), base))
		for _, s := range stale {
			c := widget.NewCheck(s.name, nil)
			c.SetChecked(strings.HasPrefix(s.reason, "merged"))
			reason := widget.NewLabel(s.reason)
			reason.TextStyle = fyne.TextStyle{Italic: true}
			checks = append(checks, c)
			names = append(names, s.name)
			list.Add(container.NewBorder(nil, nil, nil, reason, c))
		}
		deleteBtn.Enable()
	}
	rescan()

	deleteBtn.OnTapped = func() {
		var pick []string
		for i, c := range checks {
			if c.Checked {
				pick = append(pick, names[i])
			}
		}
		if len(pick) == 0 {
			status.SetText("Nothing selected.")
			return
		}
		dialog.ShowConfirm("Delete branches",
			fmt.Sprintf("Delete %d local branch(es)?\n\n%s", len(pick), strings.Join(pick, "\n")),
			func(ok bool) {
				if !ok {
					return
				}
				var failed []string
				deleted := 0
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				for _, n := range pick {
					if _, err := gitOut(ctx, repoRoot, "branch", "-d", n); err != nil {
						failed = append(failed, fmt.Sprintf("%s — %v", n, err))
					} else {
						deleted++
					}
				}
				msg := fmt.Sprintf("Deleted %d branch(es).", deleted)
				if len(failed) > 0 {
					msg += "\nCould not delete:\n" + strings.Join(failed, "\n")
				}
				status.SetText(msg)
				rescan()
				if onChanged != nil {
					onChanged()
				}
			}, parent)
	}
	pruneBtn.OnTapped = func() {
		pruneBtn.Disable()
		status.SetText("Fetching with --prune…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			_, err := gitOut(ctx, repoRoot, "fetch", "--prune", "--quiet")
			fyne.Do(func() {
				pruneBtn.Enable()
				if err != nil {
					status.SetText("Fetch failed: " + err.Error())
				} else {
					status.SetText("Fetched; list refreshed.")
				}
				rescan()
			})
		}()
	}

	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(560, 240))
	root := container.NewBorder(intro,
		container.NewVBox(widget.NewSeparator(), status, container.NewHBox(deleteBtn, pruneBtn, layout.NewSpacer(), closeBtn)),
		nil, nil, scroll)
	d = dialog.NewCustomWithoutButtons("Clean up branches — "+filepath.Base(repoRoot), root, parent)
	d.Resize(fyne.NewSize(680, 480))
	d.Show()
}

// ─────────────────────────────────────────────────────────────────────────
// Stage / unstage individual hunks
// ─────────────────────────────────────────────────────────────────────────

// splitDiffHunks splits `git diff` output for one file into its header (the
// diff --git … +++ lines) and its hunks, each starting at an "@@" line.
func splitDiffHunks(diff string) (header string, hunks []string) {
	lines := strings.SplitAfter(diff, "\n")
	var cur strings.Builder
	inHunk := false
	var hdr strings.Builder
	for _, l := range lines {
		if strings.HasPrefix(l, "@@ ") {
			if inHunk {
				hunks = append(hunks, cur.String())
				cur.Reset()
			}
			inHunk = true
		}
		if inHunk {
			cur.WriteString(l)
		} else {
			hdr.WriteString(l)
		}
	}
	if inHunk {
		hunks = append(hunks, cur.String())
	}
	return hdr.String(), hunks
}

// applyHunks applies the chosen hunks to the index (or reverses them out of
// it when unstage is true). --recount lets git recompute line counts, so a
// subset of hunks applies cleanly even though earlier ones were skipped.
func applyHunks(repoRoot, header string, chosen []string, unstage bool) error {
	patch := header + strings.Join(chosen, "")
	args := []string{"-C", repoRoot, "apply", "--cached", "--recount", "--whitespace=nowarn"}
	if unstage {
		args = append(args, "-R")
	}
	args = append(args, "-")
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// showHunkStageDialog lets the user stage (from the working-tree diff) or
// unstage (from the staged diff) individual hunks of one file.
func showHunkStageDialog(parent fyne.Window, repoRoot, rel string, onChanged func()) {
	mode := widget.NewRadioGroup([]string{"Stage unstaged changes", "Unstage staged changes"}, nil)
	mode.Horizontal = true
	mode.Selected = "Stage unstaged changes"

	list := container.NewVBox()
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	applyBtn := widget.NewButtonWithIcon("Apply selected hunks", theme.ConfirmIcon(), nil)
	applyBtn.Importance = widget.HighImportance
	allBtn := widget.NewButton("Select all", nil)
	noneBtn := widget.NewButton("Select none", nil)
	var d *dialog.CustomDialog
	closeBtn := widget.NewButton("Close", func() { d.Hide() })

	var header string
	var hunks []string
	var checks []*widget.Check

	load := func() {
		unstage := mode.Selected == "Unstage staged changes"
		args := []string{"diff", "--no-color", "-U3"}
		if unstage {
			args = append(args, "--cached")
		}
		args = append(args, "--", rel)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoRoot}, args...)...)
		out, err := cmd.Output()
		list.RemoveAll()
		checks = nil
		header, hunks = "", nil
		if err != nil {
			status.SetText("git diff failed: " + err.Error())
			applyBtn.Disable()
			return
		}
		header, hunks = splitDiffHunks(string(out))
		if len(hunks) == 0 {
			what := "unstaged changes"
			if unstage {
				what = "staged changes"
			}
			status.SetText(fmt.Sprintf("No %s with text hunks for this file (untracked and binary files can't be split into hunks).", what))
			applyBtn.Disable()
			return
		}
		status.SetText(fmt.Sprintf("%d hunk(s). Tick the ones to apply.", len(hunks)))
		applyBtn.Enable()
		for i, h := range hunks {
			c := widget.NewCheck(fmt.Sprintf("Hunk %d of %d", i+1, len(hunks)), nil)
			c.SetChecked(true)
			checks = append(checks, c)
			var segs []widget.RichTextSegment
			for _, l := range strings.Split(strings.TrimRight(h, "\n"), "\n") {
				st := widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}}
				switch {
				case strings.HasPrefix(l, "+"):
					st.ColorName = theme.ColorNameSuccess
				case strings.HasPrefix(l, "-"):
					st.ColorName = theme.ColorNameError
				case strings.HasPrefix(l, "@@"):
					st.ColorName = theme.ColorNamePrimary
				}
				segs = append(segs, &widget.TextSegment{Text: l, Style: st})
			}
			list.Add(container.NewVBox(c, widget.NewRichText(segs...), widget.NewSeparator()))
		}
	}
	mode.OnChanged = func(string) { load() }
	load()

	setAll := func(v bool) {
		for _, c := range checks {
			c.SetChecked(v)
		}
	}
	allBtn.OnTapped = func() { setAll(true) }
	noneBtn.OnTapped = func() { setAll(false) }
	applyBtn.OnTapped = func() {
		var chosen []string
		for i, c := range checks {
			if c.Checked {
				chosen = append(chosen, hunks[i])
			}
		}
		if len(chosen) == 0 {
			status.SetText("No hunks selected.")
			return
		}
		unstage := mode.Selected == "Unstage staged changes"
		if err := applyHunks(repoRoot, header, chosen, unstage); err != nil {
			status.SetText("Could not apply: " + err.Error())
			return
		}
		verb := "Staged"
		if unstage {
			verb = "Unstaged"
		}
		load()
		status.SetText(fmt.Sprintf("%s %d hunk(s).", verb, len(chosen)))
		if onChanged != nil {
			onChanged()
		}
	}

	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(640, 300))
	root := container.NewBorder(
		container.NewVBox(mode, container.NewHBox(allBtn, noneBtn), widget.NewSeparator()),
		container.NewVBox(widget.NewSeparator(), status, container.NewHBox(applyBtn, layout.NewSpacer(), closeBtn)),
		nil, nil, scroll)
	d = dialog.NewCustomWithoutButtons("Stage hunks — "+rel, root, parent)
	d.Resize(fyne.NewSize(820, 640))
	d.Show()
}
