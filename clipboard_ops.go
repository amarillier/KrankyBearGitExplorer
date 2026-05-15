package main

import (
	"strings"

	"fyne.io/fyne/v2/widget"
)

func clipOneLine(s string) string {
	return strings.TrimSuffix(s, "\n")
}

func (v *diffView) copyToClipboard(text string) {
	if v.win == nil {
		return
	}
	v.win.Clipboard().SetContent(text)
}

func (v *diffView) copyLeftLineAtRow(rowID widget.ListItemID) {
	if v.model == nil || rowID < 0 || rowID >= len(v.model.Rows) {
		return
	}
	dr := v.model.Rows[rowID]
	if dr.LeftLineNo <= 0 {
		return
	}
	v.copyToClipboard(clipOneLine(dr.Left))
}

func (v *diffView) copyRightLineAtRow(rowID widget.ListItemID) {
	if v.model == nil || rowID < 0 || rowID >= len(v.model.Rows) {
		return
	}
	dr := v.model.Rows[rowID]
	if dr.RightLineNo <= 0 {
		return
	}
	v.copyToClipboard(clipOneLine(dr.Right))
}

// copyAlignedRowClipboard copies the selected row’s real lines: one or both sides,
// joined with a newline when both exist (no trailing newline at end).
func (v *diffView) copyAlignedRowClipboard(rowID widget.ListItemID) {
	if v.model == nil || rowID < 0 || rowID >= len(v.model.Rows) {
		return
	}
	dr := v.model.Rows[rowID]
	var parts []string
	if dr.LeftLineNo > 0 {
		parts = append(parts, clipOneLine(dr.Left))
	}
	if dr.RightLineNo > 0 {
		parts = append(parts, clipOneLine(dr.Right))
	}
	if len(parts) == 0 {
		return
	}
	v.copyToClipboard(strings.Join(parts, "\n"))
}

func (v *diffView) copySelectedRowToClipboard() {
	if !v.hasDiffSelection {
		return
	}
	v.copyAlignedRowClipboard(v.selectedDiffRow)
}

func (v *diffView) copySelectedLeftLine() {
	if !v.hasDiffSelection || v.model == nil {
		return
	}
	v.copyLeftLineAtRow(v.selectedDiffRow)
}

func (v *diffView) copySelectedRightLine() {
	if !v.hasDiffSelection || v.model == nil {
		return
	}
	v.copyRightLineAtRow(v.selectedDiffRow)
}

func (v *diffView) swapSides() {
	if v.win == nil {
		return
	}
	v.clearEditHistory()
	v.leftP, v.rightP = v.rightP, v.leftP
	v.leftT, v.rightT = v.rightT, v.leftT
	v.leftDirty, v.rightDirty = v.rightDirty, v.leftDirty
	// Read-only follows the content across the swap so the HEAD buffer stays
	// protected on whichever side it now lives.
	v.leftReadOnly, v.rightReadOnly = v.rightReadOnly, v.leftReadOnly
	v.recompute()
	if v.syncScrollOn && v.leftList != nil && v.rightList != nil {
		v.syncScrollPrevL = v.leftList.GetScrollOffset()
		v.syncScrollPrevR = v.rightList.GetScrollOffset()
	}
	v.refreshMainToolbar()
	v.refreshMainMenu()
}
