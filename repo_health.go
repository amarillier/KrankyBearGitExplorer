package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// repoHealthStats holds the numeric output of `git count-objects -v`.
type repoHealthStats struct {
	Count         int // loose object count
	Size          int // loose total size in KiB
	InPack        int // packed object count
	Packs         int // pack file count
	SizePack      int // total pack size on disk in KiB
	PrunePackable int // loose objects also present in packs
	Garbage       int // garbage file count
	SizeGarbage   int // total garbage size in KiB
}

// repoHealthIssue is one entry from `git fsck` output. Status is one of:
// dangling, unreachable, missing, broken, or "other" for diagnostic lines
// that don't fit the standard "<status> <type> <hash>" shape (e.g. broken
// link from / to ...).
type repoHealthIssue struct {
	Status string
	Type   string
	Hash   string
	Raw    string
}

type repoHealthReport struct {
	RepoRoot                              string
	Stats                                 repoHealthStats
	Issues                                []repoHealthIssue
	Dangling, Unreachable, Missing, Broken int

	// Repository overview — filled in by gatherRepoSummary (go-git based,
	// independent of the count-objects / fsck shell-outs above). All fields
	// are best-effort: when go-git can't read something (e.g. a freshly
	// init'd repo with no commits) the corresponding field stays zero/empty
	// and the renderer hides it.
	HeadBranch       string    // short name of HEAD branch, or "(detached)" / "(no commits)"
	HeadSHA          string    // short SHA of HEAD commit, "" if unborn
	LastCommitSubj   string
	LastCommitAuthor string
	LastCommitWhen   time.Time // zero when there is no HEAD commit
	BranchCount      int
	TagCount         int
	RemoteCount      int
}

// gatherRepoHealth shells out to `git count-objects -v` and `git fsck
// --no-progress`, parses the line-based output, and (when a go-git handle
// is available) layers on a repository overview via gatherRepoSummary.
// Returns a non-nil error only when git itself is missing or returns an
// unexpected error code (fsck returning non-zero because issues were found
// is normal and not an error).
func gatherRepoHealth(repo *git.Repository, repoRoot string) (*repoHealthReport, error) {
	report := &repoHealthReport{RepoRoot: repoRoot}

	coBytes, err := exec.Command("git", "-C", repoRoot, "count-objects", "-v").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git count-objects: %w\n%s", err, strings.TrimSpace(string(coBytes)))
	}
	report.Stats = parseCountObjects(string(coBytes))

	fsckBytes, err := exec.Command("git", "-C", repoRoot, "fsck", "--no-progress").CombinedOutput()
	if err != nil {
		// fsck exits non-zero when it finds problems — that's not a tool
		// failure, just informational. Anything else (e.g. git binary
		// missing) is a real error.
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("git fsck: %w", err)
		}
	}
	report.Issues = parseFsck(string(fsckBytes))
	for _, i := range report.Issues {
		switch i.Status {
		case "dangling":
			report.Dangling++
		case "unreachable":
			report.Unreachable++
		case "missing":
			report.Missing++
		case "broken":
			report.Broken++
		}
	}

	if repo != nil {
		gatherRepoSummary(repo, report)
	}
	return report, nil
}

// gatherRepoSummary populates the Repository-overview fields via go-git:
// HEAD branch + commit, plus counts of local branches / tags / remotes.
// Best-effort — silently leaves fields zero on any read error so a partially-
// borked repo can still render the rest of the health report.
func gatherRepoSummary(repo *git.Repository, r *repoHealthReport) {
	if head, err := repo.Head(); err == nil {
		r.HeadBranch = head.Name().Short()
		hash := head.Hash().String()
		if len(hash) > 7 {
			r.HeadSHA = hash[:7]
		} else {
			r.HeadSHA = hash
		}
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			r.LastCommitSubj = firstLine(commit.Message)
			r.LastCommitAuthor = commit.Author.Name
			r.LastCommitWhen = commit.Author.When
		}
	} else {
		r.HeadBranch = "(no commits)"
	}

	r.BranchCount = countRefs(repo.Branches)
	r.TagCount = countRefs(repo.Tags)
	if remotes, err := repo.Remotes(); err == nil {
		r.RemoteCount = len(remotes)
	}
}

// countRefs walks a ReferenceIter factory and returns the number of refs
// yielded. Defensive against an iterator-returning error (returns 0).
func countRefs(fn func() (storer.ReferenceIter, error)) int {
	iter, err := fn()
	if err != nil {
		return 0
	}
	defer iter.Close()
	n := 0
	_ = iter.ForEach(func(_ *plumbing.Reference) error {
		n++
		return nil
	})
	return n
}

func parseCountObjects(s string) repoHealthStats {
	var st repoHealthStats
	for _, line := range strings.Split(s, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		switch key {
		case "count":
			st.Count = val
		case "size":
			st.Size = val
		case "in-pack":
			st.InPack = val
		case "packs":
			st.Packs = val
		case "size-pack":
			st.SizePack = val
		case "prune-packable":
			st.PrunePackable = val
		case "garbage":
			st.Garbage = val
		case "size-garbage":
			st.SizeGarbage = val
		}
	}
	return st
}

func parseFsck(s string) []repoHealthIssue {
	var out []repoHealthIssue
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// fsck progress noise should be suppressed by --no-progress, but be
		// defensive against version variation.
		if strings.HasPrefix(line, "Checking ") {
			continue
		}
		// "broken link from <obj> to <obj>" doesn't fit the simple shape;
		// preserve it under a "broken" status.
		if strings.HasPrefix(line, "broken link") {
			out = append(out, repoHealthIssue{Status: "broken", Raw: line})
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			out = append(out, repoHealthIssue{Status: "other", Raw: line})
			continue
		}
		out = append(out, repoHealthIssue{
			Status: fields[0],
			Type:   fields[1],
			Hash:   fields[2],
			Raw:    line,
		})
	}
	return out
}

// healthHints returns the user-facing prose hints derived from the report.
// Pure read-only — every recommendation is a CLI command the user runs
// themselves; the explorer never invokes `git gc` or anything else that
// writes to the repo.
func healthHints(r *repoHealthReport) []string {
	var hints []string
	if r.Stats.Count >= 100 {
		hints = append(hints, fmt.Sprintf(
			"%d loose objects — consider running `git -C %q gc` in your terminal to compress them into pack files.",
			r.Stats.Count, r.RepoRoot))
	}
	if r.Stats.Garbage > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d garbage file(s) detected (%d KiB). `git -C %q gc` cleans these up.",
			r.Stats.Garbage, r.Stats.SizeGarbage, r.RepoRoot))
	}
	if r.Stats.PrunePackable > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d loose objects also exist in packs (prunable). `git -C %q prune-packed` reclaims that disk space.",
			r.Stats.PrunePackable, r.RepoRoot))
	}
	if r.Dangling > 0 || r.Unreachable > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d dangling/unreachable object(s) — usually harmless residue from staged-then-discarded edits or rebased/amended commits. `git gc` prunes them after the gc.reflogExpire deadline (default 2 weeks for reflog, 90 days for unreachable).",
			r.Dangling+r.Unreachable))
	}
	if r.Missing > 0 || r.Broken > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d missing / %d broken object(s) — this can indicate corruption. Try `git -C %q fsck --full` for detail, or restore from a fresh remote clone if the damage is irrecoverable.",
			r.Missing, r.Broken, r.RepoRoot))
	}
	if len(hints) == 0 {
		hints = append(hints, "Repo looks healthy — no actions recommended.")
	}
	return hints
}

// renderHealthReportText serialises the report as plain text suitable for the
// clipboard (Copy report button) — useful for sticking into a bug report or
// a teammate's chat window.
func renderHealthReportText(r *repoHealthReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repo Health Report\n")
	fmt.Fprintf(&b, "==================\n")
	fmt.Fprintf(&b, "Repo: %s\n\n", r.RepoRoot)

	fmt.Fprintf(&b, "Repository overview\n")
	fmt.Fprintf(&b, "-------------------\n")
	fmt.Fprintf(&b, "HEAD branch:           %s\n", r.HeadBranch)
	if r.HeadSHA != "" {
		fmt.Fprintf(&b, "HEAD commit:           %s\n", r.HeadSHA)
	}
	if !r.LastCommitWhen.IsZero() {
		fmt.Fprintf(&b, "Last commit:           %s  (%s)\n",
			r.LastCommitWhen.Format("2006-01-02 15:04 MST"),
			humanRelativeTime(r.LastCommitWhen))
		fmt.Fprintf(&b, "Last commit subject:   %s\n", r.LastCommitSubj)
		fmt.Fprintf(&b, "Last commit author:    %s\n", r.LastCommitAuthor)
	}
	fmt.Fprintf(&b, "Local branches:        %d\n", r.BranchCount)
	fmt.Fprintf(&b, "Tags:                  %d\n", r.TagCount)
	fmt.Fprintf(&b, "Remotes:               %d\n\n", r.RemoteCount)

	fmt.Fprintf(&b, "Object database statistics\n")
	fmt.Fprintf(&b, "--------------------------\n")
	fmt.Fprintf(&b, "Loose objects:                    %d\n", r.Stats.Count)
	fmt.Fprintf(&b, "Loose size on disk:               %d KiB\n", r.Stats.Size)
	fmt.Fprintf(&b, "Packed objects:                   %d\n", r.Stats.InPack)
	fmt.Fprintf(&b, "Pack files:                       %d\n", r.Stats.Packs)
	fmt.Fprintf(&b, "Pack size on disk:                %d KiB\n", r.Stats.SizePack)
	fmt.Fprintf(&b, "Loose objects already in packs:   %d\n", r.Stats.PrunePackable)
	fmt.Fprintf(&b, "Garbage files:                    %d\n", r.Stats.Garbage)
	fmt.Fprintf(&b, "Garbage size:                     %d KiB\n\n", r.Stats.SizeGarbage)

	fmt.Fprintf(&b, "Verify (git fsck)\n")
	fmt.Fprintf(&b, "-----------------\n")
	if len(r.Issues) == 0 {
		fmt.Fprintf(&b, "No issues — clean object database.\n\n")
	} else {
		fmt.Fprintf(&b, "%d dangling · %d unreachable · %d missing · %d broken\n\n",
			r.Dangling, r.Unreachable, r.Missing, r.Broken)
		for _, i := range r.Issues {
			fmt.Fprintf(&b, "  %s\n", i.Raw)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "Hints\n")
	fmt.Fprintf(&b, "-----\n")
	for _, h := range healthHints(r) {
		fmt.Fprintf(&b, "- %s\n", h)
	}
	return b.String()
}

// showRepoHealth gathers + displays the report in a non-modal custom dialog.
// Read-only — there is intentionally no "Run gc" button. The user copies
// the report or copies a suggested command and runs it themselves.
func showRepoHealth(a fyne.App, parent fyne.Window, repo *git.Repository, repoRoot string) {
	report, err := gatherRepoHealth(repo, repoRoot)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}

	// --- repository overview ---
	type kv struct{ k, v string }
	overviewRows := []kv{
		{"HEAD branch", report.HeadBranch},
	}
	if report.HeadSHA != "" {
		overviewRows = append(overviewRows, kv{"HEAD commit", report.HeadSHA})
	}
	if !report.LastCommitWhen.IsZero() {
		when := fmt.Sprintf("%s  (%s)",
			report.LastCommitWhen.Format("2006-01-02 15:04 MST"),
			humanRelativeTime(report.LastCommitWhen))
		overviewRows = append(overviewRows,
			kv{"Last commit", when},
			kv{"Last commit subject", report.LastCommitSubj},
			kv{"Last commit author", report.LastCommitAuthor},
		)
	}
	overviewRows = append(overviewRows,
		kv{"Local branches", fmt.Sprintf("%d", report.BranchCount)},
		kv{"Tags", fmt.Sprintf("%d", report.TagCount)},
		kv{"Remotes", fmt.Sprintf("%d", report.RemoteCount)},
	)
	overviewGrid := container.NewGridWithColumns(2)
	for _, row := range overviewRows {
		overviewGrid.Add(widget.NewLabel(row.k))
		rv := widget.NewLabel(row.v)
		rv.Alignment = fyne.TextAlignTrailing
		rv.Wrapping = fyne.TextWrapOff
		rv.Truncation = fyne.TextTruncateEllipsis
		overviewGrid.Add(rv)
	}

	// --- stats grid ---
	rows := []kv{
		{"Loose objects", fmt.Sprintf("%d", report.Stats.Count)},
		{"Loose size on disk", fmt.Sprintf("%d KiB", report.Stats.Size)},
		{"Packed objects", fmt.Sprintf("%d", report.Stats.InPack)},
		{"Pack files", fmt.Sprintf("%d", report.Stats.Packs)},
		{"Pack size on disk", fmt.Sprintf("%d KiB", report.Stats.SizePack)},
		{"Loose objects already in packs", fmt.Sprintf("%d", report.Stats.PrunePackable)},
		{"Garbage files", fmt.Sprintf("%d", report.Stats.Garbage)},
		{"Garbage size", fmt.Sprintf("%d KiB", report.Stats.SizeGarbage)},
	}
	statsGrid := container.NewGridWithColumns(2)
	for _, row := range rows {
		statsGrid.Add(widget.NewLabel(row.k))
		rv := widget.NewLabel(row.v)
		rv.Alignment = fyne.TextAlignTrailing
		statsGrid.Add(rv)
	}

	// --- verify section ---
	var verifyContent fyne.CanvasObject
	if len(report.Issues) == 0 {
		ok := widget.NewLabel("No issues — clean object database.")
		verifyContent = ok
	} else {
		summary := widget.NewLabel(fmt.Sprintf(
			"%d dangling · %d unreachable · %d missing · %d broken",
			report.Dangling, report.Unreachable, report.Missing, report.Broken,
		))
		summary.TextStyle = fyne.TextStyle{Bold: true}

		const maxShow = 10
		var lines []string
		n := len(report.Issues)
		for i := 0; i < n && i < maxShow; i++ {
			lines = append(lines, report.Issues[i].Raw)
		}
		if n > maxShow {
			lines = append(lines, fmt.Sprintf("…and %d more (run `git -C %q fsck --no-progress` for the full list)", n-maxShow, repoRoot))
		}
		examples := widget.NewLabel(strings.Join(lines, "\n"))
		examples.TextStyle = fyne.TextStyle{Monospace: true}
		examples.Wrapping = fyne.TextWrapOff
		verifyContent = container.NewVBox(summary, examples)
	}

	// --- hints ---
	hintsVBox := container.NewVBox()
	for _, h := range healthHints(report) {
		lbl := widget.NewLabel("• " + h)
		lbl.Wrapping = fyne.TextWrapWord
		hintsVBox.Add(lbl)
	}

	// --- footer: Copy report (Close is the dialog's dismiss button) ---
	copyBtn := widget.NewButton("Copy report to clipboard", func() {
		if c := fyne.CurrentApp().Clipboard(); c != nil {
			c.SetContent(renderHealthReportText(report))
		}
	})
	footer := container.NewHBox(layout.NewSpacer(), copyBtn)

	body := container.NewVBox(
		widget.NewLabelWithStyle("Repository overview", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		overviewGrid,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Object database statistics", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		statsGrid,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Verify (git fsck)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		verifyContent,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Hints", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		hintsVBox,
	)
	scroll := container.NewVScroll(body)
	scroll.SetMinSize(fyne.NewSize(680, 420))

	content := container.NewBorder(nil, footer, nil, nil, scroll)

	title := "Repo Health — " + filepath.Base(repoRoot)
	d := dialog.NewCustom(title, "Close", content, parent)
	d.Resize(fyne.NewSize(740, 580))
	d.Show()
}
