package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRaw is gitT without trimming — diffs must keep trailing whitespace.
func gitRaw(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	return dir
}

func TestFuzzyMatch(t *testing.T) {
	if ok, _ := fuzzyMatch("kbg", "/src/go/KrankyBearGitExplorer"); !ok {
		t.Error("subsequence should match")
	}
	if ok, _ := fuzzyMatch("zzz", "/src/go/KrankyBear"); ok {
		t.Error("non-match matched")
	}
	_, base := fuzzyMatch("bear", "/x/KrankyBear")
	_, path := fuzzyMatch("bear", "/bear/other")
	if base >= path {
		t.Errorf("base-name hit (%d) should outrank path hit (%d)", base, path)
	}
}

func TestNotesArePlaceholder(t *testing.T) {
	for in, want := range map[string]bool{"": true, "-": true, "\n- \n\n": true, "- Fixed a bug": false} {
		if got := notesArePlaceholder(in); got != want {
			t.Errorf("notesArePlaceholder(%q)=%v want %v", in, got, want)
		}
	}
}

func TestHunkStageAndUnstage(t *testing.T) {
	dir := newTestRepo(t)
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		b.WriteString("line " + strings.Repeat("x", i%3) + "\n")
	}
	orig := b.String()
	f := filepath.Join(dir, "f.txt")
	os.WriteFile(f, []byte(orig), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")

	lines := strings.Split(orig, "\n")
	lines[1] = "CHANGED TOP"
	lines[35] = "CHANGED BOTTOM"
	os.WriteFile(f, []byte(strings.Join(lines, "\n")), 0o644)

	diff := gitRaw(t, dir, "diff", "--no-color", "-U3", "--", "f.txt")
	header, hunks := splitDiffHunks(diff)
	if len(hunks) != 2 {
		t.Fatalf("want 2 hunks, got %d", len(hunks))
	}
	// Stage only the second hunk.
	if err := applyHunks(dir, header, hunks[1:], false); err != nil {
		t.Fatal(err)
	}
	staged := gitT(t, dir, "diff", "--cached", "--no-color")
	if !strings.Contains(staged, "CHANGED BOTTOM") || strings.Contains(staged, "CHANGED TOP") {
		t.Fatalf("wrong hunk staged:\n%s", staged)
	}
	// Working tree must be untouched.
	if got, _ := os.ReadFile(f); !strings.Contains(string(got), "CHANGED TOP") {
		t.Fatal("working tree was modified")
	}
	// Unstage it again.
	sdiff := gitRaw(t, dir, "diff", "--cached", "--no-color", "-U3")
	h2, hk2 := splitDiffHunks(sdiff)
	if err := applyHunks(dir, h2, hk2, true); err != nil {
		t.Fatal(err)
	}
	if out := gitT(t, dir, "diff", "--cached", "--no-color"); out != "" {
		t.Fatalf("index not clean after unstage:\n%s", out)
	}
}

func TestFindStaleBranches(t *testing.T) {
	dir := newTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	gitT(t, dir, "branch", "merged-feature") // same commit as main → merged
	gitT(t, dir, "checkout", "-qb", "unmerged")
	os.WriteFile(filepath.Join(dir, "b"), []byte("b"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "work")
	gitT(t, dir, "checkout", "-q", "main")

	base, stale, err := findStaleBranches(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if base != "main" {
		t.Errorf("base=%q", base)
	}
	var names []string
	for _, s := range stale {
		names = append(names, s.name)
	}
	if len(names) != 1 || names[0] != "merged-feature" {
		t.Errorf("stale=%v, want [merged-feature]", names)
	}
}
