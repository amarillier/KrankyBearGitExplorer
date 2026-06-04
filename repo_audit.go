package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// repo_audit.go implements "Audit Local Repos…" — a read-only sweep over the
// same source folders the dep-scan "Scan All Repos" feature uses, reporting
// repos that are out of sync with a remote (or have no remote at all),
// uncommitted work, or were initialized but never committed. It's purely
// informational — no push/commit actions are offered — so it stays a safe
// "what have I left lying around?" overview. Discovery and the per-repo skip
// list are shared with dep-scan via effectiveScanRepos.

// repoAuditEntry is the classification of a single repository. A repo can carry
// more than one flag (e.g. local-only AND uncommitted); empty repos are only
// ever flagged empty, since the other checks aren't meaningful without commits.
type repoAuditEntry struct {
	path        string
	empty       bool // git init'd but no commits yet
	localOnly   bool // has commits but no remote configured
	uncommitted bool // dirty worktree (modified or untracked files)
	unpushed    bool // has a remote, but current branch is ahead / has no upstream
	syncDetail  string
}

// clean reports whether the repo has nothing worth flagging.
func (e repoAuditEntry) clean() bool {
	return !e.empty && !e.localOnly && !e.uncommitted && !e.unpushed
}

// repoAuditReport is the aggregated result of a sweep, ready to render.
type repoAuditReport struct {
	entries []repoAuditEntry
	total   int
	clean   int
	elapsed time.Duration
}

// auditRemoteNames returns the configured remote names for a repo at repoRoot,
// via the git CLI (we have a path, not an opened *git.Repository here). Empty
// on error or when the repo has no remotes.
func auditRemoteNames(repoRoot string) []string {
	out, err := exec.Command("git", "-C", repoRoot, "remote").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			names = append(names, s)
		}
	}
	return names
}

// commitsAhead counts commits on HEAD not yet on upstream (upstream..HEAD).
// Returns 0 on error, which is the safe "nothing to flag" default.
func commitsAhead(repoRoot, upstream string) int {
	out, err := exec.Command("git", "-C", repoRoot, "rev-list", "--count", upstream+"..HEAD").Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// classifyRepoAudit inspects a single repo and returns its audit entry. Uses
// the same low-level helpers (hasAnyCommits, hasAnyChanges, currentBranchName,
// branchUpstream) as the commit/push dialogs, so its verdict matches what those
// flows would show.
func classifyRepoAudit(repoRoot string) repoAuditEntry {
	e := repoAuditEntry{path: repoRoot}

	if !hasAnyCommits(repoRoot) {
		e.empty = true
		return e
	}

	remotes := auditRemoteNames(repoRoot)
	if len(remotes) == 0 {
		e.localOnly = true
	}
	if hasAnyChanges(repoRoot) {
		e.uncommitted = true
	}

	// Sync state is only meaningful when there's somewhere to push to. A
	// local-only repo is already flagged above, so don't double-report it.
	if len(remotes) > 0 {
		if branch := currentBranchName(repoRoot); branch != "" {
			if up := branchUpstream(repoRoot, branch); up == "" {
				e.unpushed = true
				e.syncDetail = fmt.Sprintf("branch %q has no upstream set", branch)
			} else if n := commitsAhead(repoRoot, up); n > 0 {
				e.unpushed = true
				e.syncDetail = fmt.Sprintf("%d commit(s) ahead of %s, not yet pushed", n, up)
			}
		}
		// Detached HEAD: no branch to reason about, so we leave sync alone.
	}

	return e
}

// buildRepoAuditReport classifies every repo and tallies the clean count.
func buildRepoAuditReport(repos []string) repoAuditReport {
	r := repoAuditReport{total: len(repos)}
	for _, repo := range repos {
		e := classifyRepoAudit(repo)
		if e.clean() {
			r.clean++
			continue
		}
		r.entries = append(r.entries, e)
	}
	return r
}

// renderRepoAuditReport turns the report into the read-only text shown in the
// dialog. Pure function (no UI, no git) so it's straightforward to unit-test.
// Repos are grouped into the four buckets; a repo with multiple issues appears
// under each bucket it matches, which is intentional — each section then reads
// as a complete answer to "which repos have <this> problem?".
func renderRepoAuditReport(r repoAuditReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Local Repo Audit — %d repositories scanned\n", r.total)
	if r.elapsed > 0 {
		fmt.Fprintf(&b, "Elapsed: %s\n", r.elapsed)
	}
	b.WriteString("\n")

	if len(r.entries) == 0 {
		fmt.Fprintf(&b, "✓ All %d repositories are committed and in sync — nothing to report.\n", r.total)
		return b.String()
	}

	bucket := func(title, note string, match func(repoAuditEntry) bool, detail func(repoAuditEntry) string) {
		var lines []string
		for _, e := range r.entries {
			if match(e) {
				line := "  • " + e.path
				if detail != nil {
					if d := detail(e); d != "" {
						line += " — " + d
					}
				}
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s — %d\n", title, len(lines))
		if note != "" {
			fmt.Fprintf(&b, "  (%s)\n", note)
		}
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n\n")
	}

	bucket("⚠ Local-only (no remote configured)",
		"committed locally but not pushed anywhere",
		func(e repoAuditEntry) bool { return e.localOnly }, nil)
	bucket("⬆ Unpushed work (ahead of remote)", "",
		func(e repoAuditEntry) bool { return e.unpushed },
		func(e repoAuditEntry) string { return e.syncDetail })
	bucket("✎ Uncommitted changes (work in progress)", "",
		func(e repoAuditEntry) bool { return e.uncommitted }, nil)
	bucket("○ Empty (initialized, no commits yet)", "",
		func(e repoAuditEntry) bool { return e.empty }, nil)

	fmt.Fprintf(&b, "✓ %d of %d repositories clean and in sync.\n", r.clean, r.total)
	return b.String()
}

// runRepoAudit sweeps every repo in effectiveScanRepos (the dep-scan source
// folders, minus the skip list), classifies each, and shows the report. The
// classification is fast local git work, but we still run it off the UI thread
// behind a progress dialog for consistency with the other all-repos sweeps.
func runRepoAudit(a fyne.App, parent fyne.Window) {
	repos := effectiveScanRepos(a)
	if len(repos) == 0 {
		dialog.ShowConfirm("Audit Local Repos",
			"No source folders are configured yet.\n\nThe audit looks through the same folders as “Scan All Repos”. Add one or more and it'll discover the git repos under them.\n\nOpen the configuration now?",
			func(yes bool) {
				if yes {
					showDepScanConfig(a, parent)
				}
			}, parent)
		return
	}

	progBar := widget.NewProgressBarInfinite()
	progLbl := widget.NewLabel(fmt.Sprintf("Auditing %d repositories for local-only, unpushed and uncommitted state…", len(repos)))
	progLbl.Wrapping = fyne.TextWrapWord
	progDlg := dialog.NewCustom("Audit Local Repos", "Hide (continues in background)", container.NewVBox(progLbl, progBar), parent)
	progDlg.Show()

	started := time.Now()
	go func() {
		report := buildRepoAuditReport(repos)
		report.elapsed = time.Since(started).Round(time.Millisecond)
		text := renderRepoAuditReport(report)
		fyne.Do(func() {
			progDlg.Hide()
			// Second fyne.Do enqueues for the next tick, so the report's scroll
			// lays out against a clean canvas — same belt-and-braces the
			// dep-scan sweep uses.
			fyne.Do(func() {
				showRepoAuditDialog(parent, text)
			})
		})
	}()
}

// showRepoAuditDialog renders the audit text read-only, mirroring the dep-scan
// report dialog (monospace MultiLineEntry in a VScroll, with a copy button).
func showRepoAuditDialog(parent fyne.Window, report string) {
	output := widget.NewMultiLineEntry()
	output.TextStyle = fyne.TextStyle{Monospace: true}
	output.Wrapping = fyne.TextWrapWord
	output.SetText(report)
	scroll := container.NewVScroll(output)
	scroll.SetMinSize(fyne.NewSize(760, 460))

	copyBtn := widget.NewButton("Copy report to clipboard", func() {
		if c := fyne.CurrentApp().Clipboard(); c != nil {
			c.SetContent(report)
		}
	})
	footer := container.NewHBox(layout.NewSpacer(), copyBtn)

	content := container.NewBorder(nil, footer, nil, nil, scroll)
	d := dialog.NewCustom("Local Repo Audit", "Close", content, parent)
	d.Resize(fyne.NewSize(840, 600))
	d.Show()
}
