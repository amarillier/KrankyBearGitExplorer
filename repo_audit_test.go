package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRepoAuditReportClean(t *testing.T) {
	got := renderRepoAuditReport(repoAuditReport{total: 5, clean: 5})
	if !strings.Contains(got, "All 5 repositories are committed and in sync") {
		t.Errorf("clean report missing all-clear line:\n%s", got)
	}
}

func TestRenderRepoAuditReportBuckets(t *testing.T) {
	r := repoAuditReport{
		total: 4,
		clean: 1,
		entries: []repoAuditEntry{
			{path: "/src/local", localOnly: true},
			{path: "/src/ahead", unpushed: true, syncDetail: "2 commit(s) ahead of origin/main, not yet pushed"},
			// One repo with two issues — must appear in both buckets.
			{path: "/src/wip", localOnly: true, uncommitted: true},
			{path: "/src/fresh", empty: true},
		},
	}
	got := renderRepoAuditReport(r)

	for _, want := range []string{
		"Local-only (no remote configured) — 2", // /src/local + /src/wip
		"/src/local",
		"Unpushed work (ahead of remote) — 1",
		"2 commit(s) ahead of origin/main, not yet pushed",
		"Uncommitted changes (work in progress) — 1",
		"/src/wip",
		"Empty (initialized, no commits yet) — 1",
		"/src/fresh",
		"1 of 4 repositories clean and in sync",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
}

// TestClassifyRepoAudit exercises the classifier against real git repos. Skips
// when git isn't on PATH so the unit suite still passes in a bare environment.
func TestClassifyRepoAudit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	git := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@e.x",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@e.x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(t *testing.T, dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Empty: init'd, no commits.
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init", "-q")
		e := classifyRepoAudit(dir)
		if !e.empty || e.clean() {
			t.Errorf("expected empty and not clean, got %+v", e)
		}
	})

	// Local-only: has a commit, no remote.
	t.Run("local-only", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init", "-q")
		write(t, dir, "a.txt", "hi")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "first")
		e := classifyRepoAudit(dir)
		if !e.localOnly || e.unpushed || e.empty {
			t.Errorf("expected local-only only, got %+v", e)
		}
	})

	// Uncommitted: committed repo with an untracked file (also local-only here).
	t.Run("uncommitted", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init", "-q")
		write(t, dir, "a.txt", "hi")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", "first")
		write(t, dir, "b.txt", "untracked")
		e := classifyRepoAudit(dir)
		if !e.uncommitted {
			t.Errorf("expected uncommitted, got %+v", e)
		}
	})
}
