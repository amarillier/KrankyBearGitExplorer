package main

import (
	"fmt"
	"regexp"
	"strings"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func sideText(v *diffView, side int) string {
	if side == 0 {
		return v.leftT
	}
	return v.rightT
}

// baselineSearchLine0 returns the 0-based source line index for the current selection on this side,
// and whether a row was selected. If nothing selected, ok is false and start is 0.
func baselineSearchLine0(v *diffView, side int) (start int, ok bool) {
	if v.model == nil || !v.hasDiffSelection || v.selectedDiffRow < 0 || v.selectedDiffRow >= len(v.model.Rows) {
		return 0, false
	}
	dr := v.model.Rows[v.selectedDiffRow]
	if side == 0 {
		if dr.LeftLineNo > 0 {
			return dr.LeftLineNo - 1, true
		}
		for i := v.selectedDiffRow - 1; i >= 0; i-- {
			if v.model.Rows[i].LeftLineNo > 0 {
				return v.model.Rows[i].LeftLineNo - 1, true
			}
		}
		return 0, true
	}
	if dr.RightLineNo > 0 {
		return dr.RightLineNo - 1, true
	}
	for i := v.selectedDiffRow - 1; i >= 0; i-- {
		if v.model.Rows[i].RightLineNo > 0 {
			return v.model.Rows[i].RightLineNo - 1, true
		}
	}
	return 0, true
}

func rowIDForSourceLine(v *diffView, side, line0 int) (widget.ListItemID, bool) {
	if v.model == nil {
		return 0, false
	}
	want := line0 + 1
	for i, dr := range v.model.Rows {
		if side == 0 && dr.LeftLineNo == want {
			return i, true
		}
		if side == 1 && dr.RightLineNo == want {
			return i, true
		}
	}
	return 0, false
}

func compileLineMatcher(query string, asRegex, caseSensitive bool) (func(string) bool, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("empty search text")
	}
	if asRegex {
		expr := q
		if !caseSensitive && !strings.HasPrefix(expr, "(?i)") && !strings.HasPrefix(expr, "(?I)") {
			expr = "(?i)(?:" + expr + ")"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
		return func(line string) bool { return re.MatchString(line) }, nil
	}
	if caseSensitive {
		return func(line string) bool { return strings.Contains(line, q) }, nil
	}
	ql := strings.ToLower(q)
	return func(line string) bool { return strings.Contains(strings.ToLower(line), ql) }, nil
}

func forwardSearchOrder(start int, n int, hasSel bool) []int {
	var order []int
	if hasSel {
		for i := start + 1; i < n; i++ {
			order = append(order, i)
		}
		for i := 0; i <= start; i++ {
			order = append(order, i)
		}
		return order
	}
	for i := 0; i < n; i++ {
		order = append(order, i)
	}
	return order
}

func backwardSearchOrder(start int, n int, hasSel bool) []int {
	var order []int
	if hasSel {
		for i := start - 1; i >= 0; i-- {
			order = append(order, i)
		}
		for i := n - 1; i > start; i-- {
			order = append(order, i)
		}
		return order
	}
	for i := n - 1; i >= 0; i-- {
		order = append(order, i)
	}
	return order
}

// goToSourceLine selects the diff row for this source line on the given side and scrolls both lists.
func (v *diffView) goToSourceLine(side, line0 int) {
	if v.leftList == nil || v.rightList == nil {
		return
	}
	rowID, ok := rowIDForSourceLine(v, side, line0)
	if !ok {
		return
	}
	v.syncSel = true
	v.leftList.Select(rowID)
	v.leftList.ScrollTo(rowID)
	v.rightList.Select(rowID)
	v.rightList.ScrollTo(rowID)
	v.syncSel = false
	if v.syncScrollOn {
		v.syncScrollPrevL = v.leftList.GetScrollOffset()
		v.syncScrollPrevR = v.rightList.GetScrollOffset()
	}
	v.refreshMainMenu()
}

func (v *diffView) findInPane(side int, forward bool) {
	if v.win == nil || v.model == nil || v.leftList == nil {
		return
	}
	entry := v.paneSearchEntry[side]
	if entry == nil || v.paneSearchRegex[side] == nil || v.paneSearchCase[side] == nil {
		return
	}
	query := strings.TrimSpace(entry.Text)
	if query == "" {
		return
	}
	match, err := compileLineMatcher(query, v.paneSearchRegex[side].Checked, v.paneSearchCase[side].Checked)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	lines := splitSourceLines(sideText(v, side))
	if len(lines) == 0 {
		dialog.ShowInformation("Find", "No text to search on this side.", v.win)
		return
	}
	start, hasSel := baselineSearchLine0(v, side)
	var order []int
	if forward {
		order = forwardSearchOrder(start, len(lines), hasSel)
	} else {
		order = backwardSearchOrder(start, len(lines), hasSel)
	}
	for _, i := range order {
		if match(lines[i]) {
			v.goToSourceLine(side, i)
			return
		}
	}
	dialog.ShowInformation("Find", "No matches.", v.win)
}
