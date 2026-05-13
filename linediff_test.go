package main

import (
	"strings"
	"testing"
)

func TestBuildDiffModel_duplicateLineContentNoPhantomMiddle(t *testing.T) {
	// Same line text appears twice on the right; left differs in the middle block.
	// With DiffLinesToChars dedup, this used to create bogus "middle" insert rows.
	left := "a\nb\nc\nx\n"
	right := "a\nb\nc\nfoo\nbar\nx\n"
	m := BuildDiffModel(left, right)
	var rights []string
	for _, r := range m.Rows {
		if r.RightTag != LinePadding {
			rights = append(rights, r.Right)
		}
	}
	got := strings.Join(rights, "|")
	want := strings.Join(strings.Split(strings.TrimSuffix(right, "\n"), "\n"), "|")
	if got != want {
		t.Fatalf("reconstructed right lines mismatch\ngot  %q\nwant %q", got, want)
	}
}

func TestBuildDiffModel_manyLinesNoRuneSplitRegression(t *testing.T) {
	// Forces line indices >=10 so comma-separated encoding would mis-diff rune-by-rune.
	var b strings.Builder
	for i := 0; i < 15; i++ {
		b.WriteString("L")
		b.WriteString(string(rune('0'+i%10)))
		b.WriteByte('\n')
	}
	base := b.String()
	left := base + "ONLY_LEFT\n"
	right := base + "ONLY_RIGHT\n"
	m := BuildDiffModel(left, right)
	var lefts, rights []string
	for _, r := range m.Rows {
		if r.LeftTag != LinePadding {
			lefts = append(lefts, r.Left)
		}
		if r.RightTag != LinePadding {
			rights = append(rights, r.Right)
		}
	}
	wantLeft := strings.Split(strings.TrimSuffix(left, "\n"), "\n")
	wantRight := strings.Split(strings.TrimSuffix(right, "\n"), "\n")
	if strings.Join(lefts, "|") != strings.Join(wantLeft, "|") {
		t.Fatalf("left lines\ngot  %q\nwant %q", lefts, wantLeft)
	}
	if strings.Join(rights, "|") != strings.Join(wantRight, "|") {
		t.Fatalf("right lines\ngot  %q\nwant %q", rights, wantRight)
	}
}

func TestBuildDiffModel_identicalFilesAllEqual(t *testing.T) {
	text := "package main\n\nimport \"fmt\"\n"
	m := BuildDiffModel(text, text)
	for i, r := range m.Rows {
		if r.IsChange() {
			t.Fatalf("row %d: expected equal, got leftTag=%v rightTag=%v", i, r.LeftTag, r.RightTag)
		}
	}
	if len(m.ChangeIndices) != 0 {
		t.Fatalf("ChangeIndices: got %v want empty", m.ChangeIndices)
	}
}
