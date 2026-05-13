package main

import (
	"strings"
	"testing"
)

func TestBuildUnifiedPatch_basic(t *testing.T) {
	s, err := buildUnifiedPatch("/tmp/a/foo.go", "/tmp/b/bar.go", "a\nb\nc\n", "a\nb\nx\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "--- foo.go") || !strings.Contains(s, "+++ bar.go") {
		t.Fatalf("unexpected headers:\n%s", s)
	}
	if !strings.Contains(s, "-c\n") || !strings.Contains(s, "+x\n") {
		t.Fatalf("expected change lines:\n%s", s)
	}
}

func TestPatchDisplayNames_sameBasename(t *testing.T) {
	a, b := patchDisplayNames("/x/foo.txt", "/y/foo.txt")
	if a != "left" || b != "right" {
		t.Fatalf("got %q %q", a, b)
	}
}

func TestBuildUnifiedPatch_identical(t *testing.T) {
	s, err := buildUnifiedPatch("a", "b", "same\n", "same\n")
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Fatalf("identical texts should yield empty unified diff, got %q", s)
	}
}
