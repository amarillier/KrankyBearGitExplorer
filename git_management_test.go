package main

import "testing"

// TestPushRejectedBehind pins the detector that drives the Push dialog's
// rescue row. Git prints two distinct markers for a behind/diverged
// rejection and we must catch BOTH — "(fetch first)" for the
// someone-else-pushed case (regression-guarded here: it was previously
// missed) and "(non-fast-forward)" for the amend/rewrite case.
func TestPushRejectedBehind(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		// The someone-else-pushed case — the primary "pull first" scenario.
		// Real git output uses "(fetch first)" here, NOT "(non-fast-forward)".
		{"fetch first (someone else pushed)", "! [rejected]        main -> main (fetch first)\nerror: failed to push some refs", true},
		// The amend/rewrite case — force-with-lease territory.
		{"non-fast-forward (amend)", "! [rejected]        main -> main (non-fast-forward)", true},
		{"clean push", "To github.com:amarillier/repo.git\n   abc123..def456  main -> main", false},
		{"unrelated rejection text", "Updates were rejected because the tag already exists", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pushRejectedBehind(c.out); got != c.want {
				t.Errorf("pushRejectedBehind(%q) = %v, want %v", c.out, got, c.want)
			}
		})
	}
}

// TestPullHadConflict guards the predicate that stops the pull-first
// rescue from chaining into a push when the working tree is left
// conflicted — including the autostash-pop case that can leave conflicts
// without an obvious non-zero exit.
func TestPullHadConflict(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"clean rebase", "Successfully rebased and updated refs/heads/main.", false},
		{"clean fast-forward", "Updating abc123..def456\nFast-forward\n file.go | 2 +-", false},
		{"rebase conflict", "CONFLICT (content): Merge conflict in main.go\ncould not apply 1a2b3c4...", true},
		{"could not apply only", "error: could not apply 1a2b3c4... feat: thing", true},
		{"autostash pop conflict", "Successfully rebased and updated refs/heads/main.\nApplying autostash resulted in conflicts.", true},
		{"needs merge", "error: you need to resolve your current index first\nmain.go: needs merge", true},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pullHadConflict(c.out); got != c.want {
				t.Errorf("pullHadConflict(%q) = %v, want %v", c.out, got, c.want)
			}
		})
	}
}
