package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mkRepo creates dir (relative to base) and drops a .git marker inside it so
// discoverRepos treats it as a repository. kind "dir" makes .git a directory
// (normal repo); "file" makes it a file (submodule/worktree).
func mkRepo(t *testing.T, base, dir, kind string) string {
	t.Helper()
	full := filepath.Join(base, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	dotgit := filepath.Join(full, ".git")
	if kind == "file" {
		if err := os.WriteFile(dotgit, []byte("gitdir: ../.git/modules/x\n"), 0o644); err != nil {
			t.Fatalf("write .git file: %v", err)
		}
	} else {
		if err := os.MkdirAll(dotgit, 0o755); err != nil {
			t.Fatalf("mkdir .git: %v", err)
		}
	}
	return full
}

func TestDiscoverRepos(t *testing.T) {
	base := t.TempDir()

	repoA := mkRepo(t, base, "projects/alpha", "dir")
	repoB := mkRepo(t, base, "projects/beta", "dir")
	// Submodule-style .git file.
	repoC := mkRepo(t, base, "projects/gamma", "file")
	// A nested repo *inside* an already-discovered repo must NOT be listed
	// separately — discovery stops descending once it finds a repo.
	mkRepo(t, base, "projects/alpha/nested", "dir")
	// A repo buried under a pruned dependency cache must be ignored.
	mkRepo(t, base, "projects/beta/node_modules/dep", "dir")
	// A repo deeper than maxDepth from the root must be ignored.
	mkRepo(t, base, "a/b/c/d/e/toodeep", "dir")
	// A plain non-repo directory.
	if err := os.MkdirAll(filepath.Join(base, "projects/plain"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := discoverRepos([]string{filepath.Join(base, "projects")}, depScanMaxDepth)

	want := []string{repoA, repoB, repoC}
	// discoverRepos returns sorted, cleaned absolute paths.
	for i := range want {
		want[i] = filepath.Clean(want[i])
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("discoverRepos mismatch:\n got:  %v\n want: %v", got, want)
	}
}

func TestDiscoverReposDepthBound(t *testing.T) {
	base := t.TempDir()
	deep := mkRepo(t, base, "x1/x2/x3/deeprepo", "dir")

	// maxDepth 2 from base: x1(1)/x2(2) — deeprepo is at depth 4, excluded.
	if got := discoverRepos([]string{base}, 2); len(got) != 0 {
		t.Errorf("expected no repos within depth 2, got %v", got)
	}
	// maxDepth 4 reaches it.
	got := discoverRepos([]string{base}, 4)
	if len(got) != 1 || got[0] != filepath.Clean(deep) {
		t.Errorf("expected [%s] within depth 4, got %v", deep, got)
	}
}

func TestDiscoverReposDedup(t *testing.T) {
	base := t.TempDir()
	repo := mkRepo(t, base, "only", "dir")
	// Overlapping roots (base and base again) must not duplicate results.
	got := discoverRepos([]string{base, base}, depScanMaxDepth)
	if len(got) != 1 || got[0] != filepath.Clean(repo) {
		t.Errorf("expected dedup to [%s], got %v", repo, got)
	}
}

func TestParseDepScanVulnCount(t *testing.T) {
	cases := []struct {
		report string
		want   int
	}{
		{"## Summary\n\n- Total findings (after severity filter): **0**\n\nNo vulnerabilities", 0},
		{"- Total findings (after severity filter): **7**", 7},
		{"- Total findings (after severity filter): **123**", 123},
		{"no summary line here", -1},
		{"", -1},
	}
	for _, c := range cases {
		if got := parseDepScanVulnCount(c.report); got != c.want {
			t.Errorf("parseDepScanVulnCount(%q) = %d, want %d", c.report, got, c.want)
		}
	}
}
