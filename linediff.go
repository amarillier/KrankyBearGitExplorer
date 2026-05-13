package main

import (
	"sort"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// BMP private-use line tokens: one rune per line index for indices 1..6400 (inclusive).
const lineRuneBMPStart = 0xE000
const lineRuneBMPEnd = 0xF8FF

// Supplementary line tokens for indices beyond the BMP PUA window.
const lineRuneSupStart = 0x100000

func lineIndexToRune(idx int) rune {
	if idx < 1 {
		return '\uFFFD'
	}
	if idx <= lineRuneBMPEnd-lineRuneBMPStart+1 {
		return rune(lineRuneBMPStart + idx - 1)
	}
	return rune(lineRuneSupStart + (idx - (lineRuneBMPEnd-lineRuneBMPStart+1) - 1))
}

func lineRuneToIndex(r rune) int {
	if r >= lineRuneSupStart {
		return int(r-lineRuneSupStart) + (lineRuneBMPEnd-lineRuneBMPStart + 1) + 1
	}
	if r >= lineRuneBMPStart && r <= lineRuneBMPEnd {
		return int(r-lineRuneBMPStart) + 1
	}
	return -1
}

// splitTextLines splits text into lines, each ending with '\n' (same rules as go-diff line mode).
func splitTextLines(text string) []string {
	var lines []string
	lineStart := 0
	lineEnd := -1
	for lineEnd < len(text)-1 {
		idx := strings.Index(text[lineStart:], "\n")
		if idx == -1 {
			lineEnd = len(text) - 1
		} else {
			lineEnd = lineStart + idx
		}
		lines = append(lines, text[lineStart:lineEnd+1])
		lineStart = lineEnd + 1
	}
	return lines
}

// diffLinesToOccurrenceMatchedRuneStrings encodes each line as one rune so DiffMain stays atomic.
// Tokens match across the two sides iff the line *string* is equal and it is the same k-th
// occurrence of that line within its file (k starts at 0). That fixes sergi/go-diff’s
// DiffLinesToChars dedup (wrong occurrence) while still matching identical files and aligning
// repeated identical lines sensibly. lineArray slots duplicate line text when a line appears
// multiple times — each slot is one logical occurrence position.
func diffLinesToOccurrenceMatchedRuneStrings(text1, text2 string) (enc1, enc2 string, lineArray []string) {
	lineArray = []string{""} // sentinel slot0, same convention as go-diff

	leftLines := splitTextLines(text1)
	rightLines := splitTextLines(text2)

	countL := make(map[string]int)
	countR := make(map[string]int)
	for _, ln := range leftLines {
		countL[ln]++
	}
	for _, ln := range rightLines {
		countR[ln]++
	}

	keys := make([]string, 0, len(countL)+len(countR))
	seen := make(map[string]struct{}, len(countL)+len(countR))
	for ln := range countL {
		if _, ok := seen[ln]; !ok {
			seen[ln] = struct{}{}
			keys = append(keys, ln)
		}
	}
	for ln := range countR {
		if _, ok := seen[ln]; !ok {
			seen[ln] = struct{}{}
			keys = append(keys, ln)
		}
	}
	sort.Strings(keys)

	indices := make(map[string][]int)
	for _, ln := range keys {
		n := countL[ln]
		if countR[ln] > n {
			n = countR[ln]
		}
		slot := make([]int, n)
		for i := 0; i < n; i++ {
			lineArray = append(lineArray, ln)
			slot[i] = len(lineArray) - 1
		}
		indices[ln] = slot
	}

	writeEnc := func(lines []string) []rune {
		occ := make(map[string]int)
		rs := make([]rune, 0, len(lines))
		for _, ln := range lines {
			k := occ[ln]
			occ[ln]++
			idx := indices[ln][k]
			rs = append(rs, lineIndexToRune(idx))
		}
		return rs
	}

	return string(writeEnc(leftLines)), string(writeEnc(rightLines)), lineArray
}

func hydrateDiffsFromLineRunes(diffs []diffmatchpatch.Diff, lineArray []string) []diffmatchpatch.Diff {
	hydrated := make([]diffmatchpatch.Diff, len(diffs))
	for i, d := range diffs {
		var b strings.Builder
		for _, r := range d.Text {
			idx := lineRuneToIndex(r)
			if idx >= 0 && idx < len(lineArray) {
				b.WriteString(lineArray[idx])
			}
		}
		hydrated[i] = diffmatchpatch.Diff{Type: d.Type, Text: b.String()}
	}
	return hydrated
}

// LineTag classifies how a line participates in the side-by-side view.
type LineTag byte

const (
	LineEqual LineTag = iota
	LineAdded
	LineRemoved
	LinePadding
)

// DiffRow is one aligned row in the two-pane view (same index on left and right).
// LeftLineNo / RightLineNo are 1-based source line numbers for that side; 0 means
// this row is a padding slot (the other side has an insert/delete) and has no line in the file.
type DiffRow struct {
	Left, Right string
	LeftTag     LineTag
	RightTag    LineTag
	LeftLineNo  int
	RightLineNo int
}

// DiffModel holds aligned rows and indices of rows that differ.
type DiffModel struct {
	Rows          []DiffRow
	ChangeIndices []int
}

// IsChange reports whether this row should be highlighted as part of a diff.
func (r DiffRow) IsChange() bool {
	return r.LeftTag != LineEqual || r.RightTag != LineEqual
}

// BuildDiffModel aligns two texts for side-by-side display.
func BuildDiffModel(a, b string) *DiffModel {
	a = strings.ReplaceAll(strings.ReplaceAll(a, "\r\n", "\n"), "\r", "\n")
	b = strings.ReplaceAll(strings.ReplaceAll(b, "\r\n", "\n"), "\r", "\n")

	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = 0

	ch1, ch2, lineArray := diffLinesToOccurrenceMatchedRuneStrings(a, b)
	// DiffMain ends with DiffCleanupMerge internally. Do not run DiffCleanupSemantic here:
	// it assumes natural-language text and breaks line-token encoding (one PUA rune per line)
	// by turning small equalities into delete+insert pairs and by overlap edits, which
	// misaligns panes and marks most lines as changed.
	diffs := dmp.DiffMain(ch1, ch2, false)
	diffs = hydrateDiffsFromLineRunes(diffs, lineArray)

	var rows []DiffRow
	leftLineNo, rightLineNo := 0, 0
	for _, d := range diffs {
		t := strings.ReplaceAll(strings.ReplaceAll(d.Text, "\r\n", "\n"), "\r", "\n")
		lines := strings.Split(t, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			for _, ln := range lines {
				leftLineNo++
				rightLineNo++
				rows = append(rows, DiffRow{
					Left: ln, Right: ln, LeftTag: LineEqual, RightTag: LineEqual,
					LeftLineNo: leftLineNo, RightLineNo: rightLineNo,
				})
			}
		case diffmatchpatch.DiffDelete:
			for _, ln := range lines {
				leftLineNo++
				rows = append(rows, DiffRow{
					Left: ln, Right: "", LeftTag: LineRemoved, RightTag: LinePadding,
					LeftLineNo: leftLineNo, RightLineNo: 0,
				})
			}
		case diffmatchpatch.DiffInsert:
			for _, ln := range lines {
				rightLineNo++
				rows = append(rows, DiffRow{
					Left: "", Right: ln, LeftTag: LinePadding, RightTag: LineAdded,
					LeftLineNo: 0, RightLineNo: rightLineNo,
				})
			}
		}
	}

	var changes []int
	for i, r := range rows {
		if r.IsChange() {
			changes = append(changes, i)
		}
	}
	return &DiffModel{Rows: rows, ChangeIndices: changes}
}
