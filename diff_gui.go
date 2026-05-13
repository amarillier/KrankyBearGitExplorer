package main

import (
	"fmt"
	"image/color"
	"io"
	"math"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

const appID = "com.github.amarillier.KrankyBearGitExplorer"

// diffLineRow wraps a list cell so we can handle primary tap (selection) and
// secondary tap (merge menu) without blocking the list's selection behavior.
type diffLineRow struct {
	widget.BaseWidget
	inner fyne.CanvasObject
	view  *diffView
	side  int
	rowID widget.ListItemID
}

func newDiffLineRow(view *diffView, side int, inner fyne.CanvasObject) *diffLineRow {
	r := &diffLineRow{view: view, side: side, inner: inner}
	r.ExtendBaseWidget(r)
	return r
}

func (r *diffLineRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.inner)
}

func (r *diffLineRow) Tapped(ev *fyne.PointEvent) {
	if r.view == nil {
		return
	}
	var lst *widget.List
	if r.side == 0 {
		lst = r.view.leftList
	} else {
		lst = r.view.rightList
	}
	if lst == nil {
		return
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(lst); c != nil {
		if f, ok := any(lst).(fyne.Focusable); ok {
			c.Focus(f)
		}
	}
	lst.Select(r.rowID)
}

func (r *diffLineRow) TappedSecondary(ev *fyne.PointEvent) {
	if r.view != nil {
		r.view.showMergeMenu(r.side, r.rowID, ev.AbsolutePosition)
	}
}

type diffView struct {
	app    fyne.App
	win    fyne.Window
	model  *DiffModel
	leftP  string
	rightP string
	leftT  string
	rightT string

	leftTitle  *widget.Label
	rightTitle *widget.Label
	leftList   *widget.List
	rightList  *widget.List
	leftCol    fyne.CanvasObject
	rightCol   fyne.CanvasObject

	syncSel    bool
	changeSlot int

	syncScrollOn       bool
	syncScrollApplying bool
	syncScrollPrevL    float32
	syncScrollPrevR    float32

	showLineNumbers bool
	showWhitespace  bool

	hasDiffSelection bool
	selectedDiffRow  widget.ListItemID

	leftDirty  bool
	rightDirty bool

	btnSaveLeft, btnSaveRight, btnSaveBoth *ttwidget.Button
	btnUndo, btnRedo                       *ttwidget.Button
	btnSwapSides                           *ttwidget.Button
	btnSyncScroll                          *ttwidget.Button
	btnLineNums, btnWhitespace             *ttwidget.Button

	undoStack, redoStack []editSnapshot

	// flyoutPop is a *widget.PopUp menu (merge row, recent files) so fyne-tooltip can attach to Overlays().Top().
	flyoutPop *widget.PopUp

	paneSearchEntry [2]*widget.Entry
	paneSearchRegex [2]*ttwidget.Check
	paneSearchCase  [2]*ttwidget.Check

	btnReloadPane [2]*ttwidget.Button

	mainSplit *container.Split // main left/right pane split; offset persisted on quit
}

func (v *diffView) showFlyoutMenu(menu *fyne.Menu, pos fyne.Position) {
	if v.win == nil {
		return
	}
	if v.flyoutPop != nil {
		fynetooltip.DestroyPopUpToolTipLayer(v.flyoutPop)
		v.flyoutPop = nil
	}
	menuW := widget.NewMenu(menu)
	menuW.Resize(menuW.MinSize())
	pop := widget.NewPopUp(menuW, v.win.Canvas())
	v.flyoutPop = pop
	fynetooltip.AddPopUpToolTipLayer(pop)
	menuW.OnDismiss = func() {
		fynetooltip.DestroyPopUpToolTipLayer(pop)
		if v.flyoutPop == pop {
			v.flyoutPop = nil
		}
		pop.Hide()
	}
	pop.ShowAtPosition(pos)
}

func rgbaAlpha(c color.Color, alpha uint8) color.NRGBA {
	r16, g16, b16, _ := c.RGBA()
	return color.NRGBA{R: uint8(r16 >> 8), G: uint8(g16 >> 8), B: uint8(b16 >> 8), A: alpha}
}

func colorToNRGBA(c color.Color) color.NRGBA {
	r16, g16, b16, a16 := c.RGBA()
	return color.NRGBA{R: uint8(r16 >> 8), G: uint8(g16 >> 8), B: uint8(b16 >> 8), A: uint8(a16 >> 8)}
}

// blendToward mixes b into a (t=0 → a, t=1 → b).
func blendToward(a, b color.Color, t float32) color.NRGBA {
	x := colorToNRGBA(a)
	y := colorToNRGBA(b)
	return color.NRGBA{
		R: uint8(float32(x.R)*(1-t) + float32(y.R)*t),
		G: uint8(float32(x.G)*(1-t) + float32(y.G)*t),
		B: uint8(float32(x.B)*(1-t) + float32(y.B)*t),
		A: uint8(fyne.Max(float32(x.A), float32(y.A))),
	}
}

func (v *diffView) tagBackground(tag LineTag) color.Color {
	switch tag {
	case LineAdded:
		return rgbaAlpha(theme.Color(theme.ColorNameSuccess), 0x44)
	case LineRemoved:
		return rgbaAlpha(theme.Color(theme.ColorNameError), 0x44)
	case LinePadding:
		return rgbaAlpha(theme.Color(theme.ColorNameDisabled), 0x22)
	default:
		return color.Transparent
	}
}

func (v *diffView) recompute() {
	v.model = BuildDiffModel(v.leftT, v.rightT)
	v.hasDiffSelection = false
	if v.leftList != nil {
		v.leftList.Refresh()
		v.leftList.UnselectAll()
	}
	if v.rightList != nil {
		v.rightList.Refresh()
		v.rightList.UnselectAll()
	}
	v.changeSlot = 0
	v.refreshTitles()
	if v.syncScrollOn && v.leftList != nil && v.rightList != nil {
		v.syncScrollPrevL = v.leftList.GetScrollOffset()
		v.syncScrollPrevR = v.rightList.GetScrollOffset()
	}
	v.refreshMainMenu()
}

func (v *diffView) refreshTitles() {
	lp, rp := "(none)", "(none)"
	if v.leftP != "" {
		lp = v.leftP
	}
	if v.rightP != "" {
		rp = v.rightP
	}
	v.leftTitle.SetText(lp)
	v.rightTitle.SetText(rp)
}

// diffLineCellMinHeight is the list row height: glyph box for monospace text plus padding.
func diffLineCellMinHeight() float32 {
	probe := canvas.NewText("Mg", color.Black)
	probe.TextStyle = fyne.TextStyle{Monospace: true}
	probe.TextSize = theme.TextSize()
	return probe.MinSize().Height + theme.InnerPadding()*2 + theme.LineSpacing()
}

func (v *diffView) makeLineCell(side int) func() fyne.CanvasObject {
	return func() fyne.CanvasObject {
		bg := canvas.NewRectangle(color.Transparent)
		bg.SetMinSize(fyne.NewSize(32, diffLineCellMinHeight()))
		num := widget.NewLabel("")
		num.TextStyle = fyne.TextStyle{Monospace: true}
		num.Alignment = fyne.TextAlignTrailing
		num.Truncation = fyne.TextTruncateOff
		num.Wrapping = fyne.TextWrapOff
		txt := widget.NewLabel("")
		txt.TextStyle = fyne.TextStyle{Monospace: true}
		txt.Truncation = fyne.TextTruncateEllipsis
		txt.Wrapping = fyne.TextWrapOff
		// Border: fixed-width gutter on the left, text fills the rest (HBox shared space and truncated labels).
		row := container.NewBorder(nil, nil, num, nil, txt)
		inner := container.NewMax(bg, row)
		return newDiffLineRow(v, side, inner)
	}
}

func (v *diffView) updateLineCell(side int) func(id widget.ListItemID, o fyne.CanvasObject) {
	return func(id widget.ListItemID, o fyne.CanvasObject) {
		if v.model == nil || id < 0 || id >= len(v.model.Rows) {
			return
		}
		wrap := o.(*diffLineRow)
		wrap.rowID = id
		dr := v.model.Rows[id]
		max := wrap.inner.(*fyne.Container)
		bg := max.Objects[0].(*canvas.Rectangle)
		line := max.Objects[1].(*fyne.Container)
		// NewBorder(nil,nil,num,nil,txt) stores objects as [txt, num].
		txt := line.Objects[0].(*widget.Label)
		num := line.Objects[1].(*widget.Label)

		if v.showLineNumbers {
			num.Show()
			if side == 0 {
				if dr.LeftLineNo > 0 {
					num.SetText(fmt.Sprintf("%6d ", dr.LeftLineNo))
				} else {
					num.SetText(fmt.Sprintf("%6s ", "·"))
				}
			} else {
				if dr.RightLineNo > 0 {
					num.SetText(fmt.Sprintf("%6d ", dr.RightLineNo))
				} else {
					num.SetText(fmt.Sprintf("%6s ", "·"))
				}
			}
		} else {
			num.Hide()
		}

		var text string
		var tag LineTag
		if side == 0 {
			text = dr.Left
			tag = dr.LeftTag
		} else {
			text = dr.Right
			tag = dr.RightTag
		}
		txt.SetText(formatLineForDisplay(text, v.showWhitespace))
		var rowBG color.Color
		if tag == LineEqual {
			rowBG = theme.Color(theme.ColorNameBackground)
		} else {
			rowBG = v.tagBackground(tag)
		}
		if v.hasDiffSelection && id == v.selectedDiffRow {
			rowBG = blendToward(rowBG, theme.Color(theme.ColorNameSelection), 0.5)
		}
		bg.FillColor = rowBG
		bg.Refresh()
	}
}

func (v *diffView) syncScrollAfterEdit() {
	if v.syncScrollOn && v.leftList != nil && v.rightList != nil {
		v.syncScrollPrevL = v.leftList.GetScrollOffset()
		v.syncScrollPrevR = v.rightList.GetScrollOffset()
	}
}

func (v *diffView) refreshMainToolbar() {
	if v.btnSaveLeft == nil {
		return
	}
	fyne.Do(func() {
		if v.leftP != "" && v.leftDirty {
			v.btnSaveLeft.Enable()
		} else {
			v.btnSaveLeft.Disable()
		}
		if v.rightP != "" && v.rightDirty {
			v.btnSaveRight.Enable()
		} else {
			v.btnSaveRight.Disable()
		}
		if (v.leftP != "" && v.leftDirty) || (v.rightP != "" && v.rightDirty) {
			v.btnSaveBoth.Enable()
		} else {
			v.btnSaveBoth.Disable()
		}
		if v.showLineNumbers {
			v.btnLineNums.Importance = widget.MediumImportance
		} else {
			v.btnLineNums.Importance = widget.LowImportance
		}
		v.btnLineNums.Refresh()
		if v.showWhitespace {
			v.btnWhitespace.Importance = widget.MediumImportance
		} else {
			v.btnWhitespace.Importance = widget.LowImportance
		}
		v.btnWhitespace.Refresh()
		if v.syncScrollOn {
			v.btnSyncScroll.Importance = widget.MediumImportance
		} else {
			v.btnSyncScroll.Importance = widget.LowImportance
		}
		v.btnSyncScroll.Refresh()
		v.refreshUndoRedoButtons()
		for i := 0; i < 2; i++ {
			if v.btnReloadPane[i] == nil {
				continue
			}
			if (i == 0 && v.leftP != "") || (i == 1 && v.rightP != "") {
				v.btnReloadPane[i].Enable()
			} else {
				v.btnReloadPane[i].Disable()
			}
			v.btnReloadPane[i].Refresh()
		}
	})
}

func (v *diffView) refreshUndoRedoButtons() {
	if v.btnUndo == nil || v.btnRedo == nil {
		return
	}
	if len(v.undoStack) > 0 {
		v.btnUndo.Enable()
	} else {
		v.btnUndo.Disable()
	}
	if len(v.redoStack) > 0 {
		v.btnRedo.Enable()
	} else {
		v.btnRedo.Disable()
	}
}

func (v *diffView) buildMainChromeToolbar() fyne.CanvasObject {
	v.btnSaveLeft = ttwidget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() { v.saveSideAttempt(0) })
	v.btnSaveLeft.SetToolTip("Save left file (only when it has unsaved changes)")
	v.btnSaveRight = ttwidget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() { v.saveSideAttempt(1) })
	v.btnSaveRight.SetToolTip("Save right file (only when it has unsaved changes)")
	v.btnSaveBoth = ttwidget.NewButtonWithIcon("", theme.ConfirmIcon(), func() { v.saveBothAttempt() })
	v.btnSaveBoth.SetToolTip("Save both files (only sides that are dirty)")
	v.btnUndo = ttwidget.NewButtonWithIcon("", theme.ContentUndoIcon(), func() { v.undoEdit() })
	v.btnUndo.SetToolTip("Undo last merge edit (up to 5 steps; Cmd+Z / Ctrl+Z)")
	v.btnRedo = ttwidget.NewButtonWithIcon("", theme.ContentRedoIcon(), func() { v.redoEdit() })
	v.btnRedo.SetToolTip("Redo merge edit (up to 5 steps; Shift+Cmd+Z / Ctrl+Shift+Z)")
	v.btnUndo.Disable()
	v.btnRedo.Disable()
	v.btnSwapSides = ttwidget.NewButtonWithIcon("", theme.ViewRestoreIcon(), func() { v.swapSides() })
	v.btnSwapSides.SetToolTip("Swap left and right files (paths and contents; Cmd+Shift+X / Ctrl+Shift+X)")
	v.btnSyncScroll = ttwidget.NewButtonWithIcon("", theme.MailReplyAllIcon(), func() {
		v.syncScrollOn = !v.syncScrollOn
		if v.syncScrollOn && v.leftList != nil && v.rightList != nil {
			v.syncScrollPrevL = v.leftList.GetScrollOffset()
			v.syncScrollPrevR = v.rightList.GetScrollOffset()
		}
		v.app.Preferences().SetBool(prefSyncScroll, v.syncScrollOn)
		v.refreshMainToolbar()
		v.refreshMainMenu()
	})
	v.btnSyncScroll.SetToolTip("Sync scroll: keep left and right panes at the same vertical offset when scrolling")
	v.btnLineNums = ttwidget.NewButtonWithIcon("", theme.ListIcon(), func() {
		v.showLineNumbers = !v.showLineNumbers
		v.app.Preferences().SetBool(prefShowLineNumbers, v.showLineNumbers)
		v.refreshDiffLists()
		v.refreshMainToolbar()
		v.refreshMainMenu()
	})
	v.btnLineNums.SetToolTip("Toggle source line numbers in the gutter")
	v.btnWhitespace = ttwidget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		v.showWhitespace = !v.showWhitespace
		v.app.Preferences().SetBool(prefShowWhitespace, v.showWhitespace)
		v.refreshDiffLists()
		v.refreshMainToolbar()
		v.refreshMainMenu()
	})
	v.btnWhitespace.SetToolTip("Toggle visible whitespace (· space, → tab)")
	for _, b := range []*ttwidget.Button{v.btnSaveLeft, v.btnSaveRight, v.btnSaveBoth, v.btnUndo, v.btnRedo, v.btnSwapSides, v.btnSyncScroll, v.btnLineNums, v.btnWhitespace} {
		b.Importance = widget.LowImportance
	}
	// Icon-only: save left/right/both | undo/redo | swap sides | sync scroll | line numbers | whitespace.
	return container.NewHBox(
		v.btnSaveLeft,
		v.btnSaveRight,
		v.btnSaveBoth,
		widget.NewSeparator(),
		v.btnUndo,
		v.btnRedo,
		widget.NewSeparator(),
		v.btnSwapSides,
		v.btnSyncScroll,
		v.btnLineNums,
		v.btnWhitespace,
	)
}

func (v *diffView) runApplyLeftToRightAtRow(rid widget.ListItemID) {
	if v.model == nil || v.win == nil {
		return
	}
	ll := splitSourceLines(v.leftT)
	rr := splitSourceLines(v.rightT)
	newR, ok := applyLeftToRightAtRow(v.model, rid, ll, rr)
	if !ok {
		dialog.ShowError(fmt.Errorf("cannot apply left to right for this row"), v.win)
		return
	}
	v.beginEdit()
	v.rightT = joinSourceLines(newR)
	v.rightDirty = true
	v.recompute()
	v.syncScrollAfterEdit()
	v.refreshMainToolbar()
	v.refreshMainMenu()
}

func (v *diffView) runApplyRightToLeftAtRow(rid widget.ListItemID) {
	if v.model == nil || v.win == nil {
		return
	}
	ll := splitSourceLines(v.leftT)
	rr := splitSourceLines(v.rightT)
	newL, ok := applyRightToLeftAtRow(v.model, rid, ll, rr)
	if !ok {
		dialog.ShowError(fmt.Errorf("cannot apply right to left for this row"), v.win)
		return
	}
	v.beginEdit()
	v.leftT = joinSourceLines(newL)
	v.leftDirty = true
	v.recompute()
	v.syncScrollAfterEdit()
	v.refreshMainToolbar()
	v.refreshMainMenu()
}

func (v *diffView) runDeleteLeftAtRow(rid widget.ListItemID) {
	if v.model == nil || v.win == nil {
		return
	}
	ll := splitSourceLines(v.leftT)
	newL, ok := deleteLeftLineAtRow(v.model, rid, ll)
	if !ok {
		dialog.ShowError(fmt.Errorf("no line to delete on the left for this row"), v.win)
		return
	}
	v.beginEdit()
	v.leftT = joinSourceLines(newL)
	v.leftDirty = true
	v.recompute()
	v.syncScrollAfterEdit()
	v.refreshMainToolbar()
	v.refreshMainMenu()
}

func (v *diffView) runDeleteRightAtRow(rid widget.ListItemID) {
	if v.model == nil || v.win == nil {
		return
	}
	rr := splitSourceLines(v.rightT)
	newR, ok := deleteRightLineAtRow(v.model, rid, rr)
	if !ok {
		dialog.ShowError(fmt.Errorf("no line to delete on the right for this row"), v.win)
		return
	}
	v.beginEdit()
	v.rightT = joinSourceLines(newR)
	v.rightDirty = true
	v.recompute()
	v.syncScrollAfterEdit()
	v.refreshMainToolbar()
	v.refreshMainMenu()
}

func (v *diffView) showMergeMenu(fromSide int, rowID widget.ListItemID, abs fyne.Position) {
	if v.model == nil || rowID < 0 || rowID >= len(v.model.Rows) || v.win == nil {
		return
	}
	rid := rowID
	dr := v.model.Rows[rid]
	applyL := fyne.NewMenuItem(applyLeftToRightMenuLabel(dr), func() { v.runApplyLeftToRightAtRow(rid) })
	applyL.Disabled = !canApplyLeftToRightAtRow(v.model, rid)
	applyR := fyne.NewMenuItem(applyRightToLeftMenuLabel(dr), func() { v.runApplyRightToLeftAtRow(rid) })
	applyR.Disabled = !canApplyRightToLeftAtRow(v.model, rid)
	delLeft := fyne.NewMenuItem("Delete line from left file", func() { v.runDeleteLeftAtRow(rid) })
	delRight := fyne.NewMenuItem("Delete line from right file", func() { v.runDeleteRightAtRow(rid) })
	delLeft.Disabled = dr.LeftLineNo <= 0
	delRight.Disabled = dr.RightLineNo <= 0

	copyLeft := fyne.NewMenuItem("Copy left line", func() { v.copyLeftLineAtRow(rid) })
	copyLeft.Disabled = dr.LeftLineNo <= 0
	copyRight := fyne.NewMenuItem("Copy right line", func() { v.copyRightLineAtRow(rid) })
	copyRight.Disabled = dr.RightLineNo <= 0
	copyRow := fyne.NewMenuItem("Copy aligned row", func() { v.copyAlignedRowClipboard(rid) })
	copyRow.Disabled = dr.LeftLineNo <= 0 && dr.RightLineNo <= 0

	var mergeItems []*fyne.MenuItem
	if fromSide == 0 {
		mergeItems = []*fyne.MenuItem{applyL, applyR}
	} else {
		mergeItems = []*fyne.MenuItem{applyR, applyL}
	}
	items := []*fyne.MenuItem{
		copyLeft, copyRight, copyRow,
		fyne.NewMenuItemSeparator(),
	}
	items = append(items, mergeItems...)
	var delItems []*fyne.MenuItem
	if !contextDeleteLeftDuplicatesApplyRightToLeft(dr) {
		delItems = append(delItems, delLeft)
	}
	if !contextDeleteRightDuplicatesApplyLeftToRight(dr) {
		delItems = append(delItems, delRight)
	}
	if len(delItems) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		items = append(items, delItems...)
	}
	v.showFlyoutMenu(fyne.NewMenu("", items...), abs)
}

func (v *diffView) syncFrom(_ *widget.List, dst *widget.List, id widget.ListItemID) {
	v.hasDiffSelection = true
	v.selectedDiffRow = id
	if v.syncSel {
		return
	}
	v.syncSel = true
	dst.Select(id)
	dst.ScrollTo(id)
	v.syncSel = false
	v.refreshMainMenu()
}

func (v *diffView) jumpToFileStart() {
	if v.model == nil || len(v.model.Rows) == 0 {
		return
	}
	v.changeSlot = 0
	v.syncSel = true
	v.leftList.Select(0)
	v.leftList.ScrollToTop()
	v.rightList.Select(0)
	v.rightList.ScrollToTop()
	v.syncSel = false
	if v.syncScrollOn {
		v.syncScrollPrevL = v.leftList.GetScrollOffset()
		v.syncScrollPrevR = v.rightList.GetScrollOffset()
	}
	v.refreshMainMenu()
}

func (v *diffView) jumpToFileEnd() {
	if v.model == nil || len(v.model.Rows) == 0 {
		return
	}
	last := widget.ListItemID(len(v.model.Rows) - 1)
	if len(v.model.ChangeIndices) > 0 {
		v.changeSlot = len(v.model.ChangeIndices) - 1
	}
	v.syncSel = true
	v.leftList.Select(last)
	v.leftList.ScrollToBottom()
	v.rightList.Select(last)
	v.rightList.ScrollToBottom()
	v.syncSel = false
	if v.syncScrollOn {
		v.syncScrollPrevL = v.leftList.GetScrollOffset()
		v.syncScrollPrevR = v.rightList.GetScrollOffset()
	}
	v.refreshMainMenu()
}

func (v *diffView) jumpDiff(delta int) {
	if v.model == nil || len(v.model.ChangeIndices) == 0 {
		return
	}
	v.changeSlot += delta
	if v.changeSlot < 0 {
		v.changeSlot = len(v.model.ChangeIndices) - 1
	}
	if v.changeSlot >= len(v.model.ChangeIndices) {
		v.changeSlot = 0
	}
	id := v.model.ChangeIndices[v.changeSlot]
	v.syncSel = true
	v.leftList.Select(id)
	v.leftList.ScrollTo(id)
	v.rightList.Select(id)
	v.rightList.ScrollTo(id)
	v.syncSel = false
	if v.syncScrollOn {
		v.syncScrollPrevL = v.leftList.GetScrollOffset()
		v.syncScrollPrevR = v.rightList.GetScrollOffset()
	}
	v.refreshMainMenu()
}

func abs32(x float32) float32 {
	return float32(math.Abs(float64(x)))
}

func (v *diffView) syncScrollTick() {
	if v.leftList == nil || v.rightList == nil || !v.syncScrollOn {
		return
	}
	const eps = float32(2)
	lo := v.leftList.GetScrollOffset()
	ro := v.rightList.GetScrollOffset()
	if v.syncScrollApplying {
		v.syncScrollPrevL, v.syncScrollPrevR = lo, ro
		return
	}
	leftMoved := abs32(lo-v.syncScrollPrevL) > eps
	rightMoved := abs32(ro-v.syncScrollPrevR) > eps
	switch {
	case leftMoved && !rightMoved:
		v.syncScrollApplying = true
		v.rightList.ScrollToOffset(lo)
		v.syncScrollApplying = false
	case rightMoved && !leftMoved:
		v.syncScrollApplying = true
		v.leftList.ScrollToOffset(ro)
		v.syncScrollApplying = false
	case leftMoved && rightMoved && abs32(lo-ro) > eps:
		v.syncScrollApplying = true
		v.rightList.ScrollToOffset(lo)
		v.syncScrollApplying = false
	}
	v.syncScrollPrevL = v.leftList.GetScrollOffset()
	v.syncScrollPrevR = v.rightList.GetScrollOffset()
}

func (v *diffView) syncScrollPollLoop() {
	tick := time.NewTicker(33 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		fyne.Do(v.syncScrollTick)
	}
}

func (v *diffView) readURI(u fyne.URI) (string, []byte, error) {
	rc, err := storage.Reader(u)
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", nil, err
	}
	path := u.Path()
	if path == "" {
		path = u.String()
	}
	return path, b, nil
}

func (v *diffView) completeLoad(side int, path, text string) {
	fyne.Do(func() {
		v.clearEditHistory()
		if side == 0 {
			v.leftP, v.leftT = path, text
			v.leftDirty = false
		} else {
			v.rightP, v.rightT = path, text
			v.rightDirty = false
		}
		if path != "" {
			addRecent(v.app, side, path)
		}
		v.recompute()
		v.refreshMainMenu()
		v.refreshMainToolbar()
	})
}

// reloadSideAttempt re-reads the file for this pane from disk. If there are unsaved edits, asks before discarding them.
func (v *diffView) reloadSideAttempt(side int) {
	if v.win == nil {
		return
	}
	var path string
	var dirty bool
	if side == 0 {
		path, dirty = v.leftP, v.leftDirty
	} else {
		path, dirty = v.rightP, v.rightDirty
	}
	if path == "" {
		return
	}
	doReload := func() {
		b, err := os.ReadFile(path)
		if err != nil {
			dialog.ShowError(fmt.Errorf("reload file: %w", err), v.win)
			return
		}
		v.completeLoad(side, path, string(b))
	}
	if dirty {
		dialog.ShowConfirm("Reload file",
			"This side has unsaved changes. Reload from disk and discard them?",
			func(ok bool) {
				if ok {
					doReload()
				}
			},
			v.win)
		return
	}
	doReload()
}

func (v *diffView) loadPathFromRecent(side int, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fyne.Do(func() {
			removeRecent(v.app, side, path)
			v.refreshMainMenu()
			dialog.ShowError(fmt.Errorf("could not open recent file: %w", err), v.win)
		})
		return
	}
	v.completeLoad(side, path, string(b))
}

func (v *diffView) loadSide(side int, uri fyne.URI) {
	path, b, err := v.readURI(uri)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("read file: %w", err), v.win)
		})
		return
	}
	v.completeLoad(side, path, string(b))
}

func (v *diffView) showRecentMenuForSide(side int, anchor fyne.CanvasObject) {
	if v.win == nil || anchor == nil {
		return
	}
	ap := v.app.Driver().AbsolutePositionForObject(anchor)
	sz := anchor.Size()
	v.showFlyoutMenu(v.buildRecentSubmenu(side), fyne.NewPos(ap.X, ap.Y+sz.Height))
}

func (v *diffView) openFileDialog(side int) {
	d := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, v.win) })
			return
		}
		if reader == nil {
			return
		}
		defer reader.Close()
		u := reader.URI()
		b, err := io.ReadAll(reader)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, v.win) })
			return
		}
		path := u.Path()
		if path == "" {
			path = u.String()
		}
		v.completeLoad(side, path, string(b))
	}, v.win)
	d.Show()
}

func (v *diffView) dropTarget(pos fyne.Position, uris []fyne.URI) {
	if len(uris) == 0 {
		return
	}
	d := v.app.Driver()
	apL := d.AbsolutePositionForObject(v.leftCol)
	szL := v.leftCol.Size()
	apR := d.AbsolutePositionForObject(v.rightCol)
	szR := v.rightCol.Size()

	inLeft := pos.X >= apL.X && pos.X < apL.X+szL.Width && pos.Y >= apL.Y && pos.Y < apL.Y+szL.Height
	inRight := pos.X >= apR.X && pos.X < apR.X+szR.Width && pos.Y >= apR.Y && pos.Y < apR.Y+szR.Height

	side := -1
	switch {
	case inLeft && !inRight:
		side = 0
	case inRight && !inLeft:
		side = 1
	case inLeft && inRight:
		if pos.X < apL.X+szL.Width/2 {
			side = 0
		} else {
			side = 1
		}
	default:
		if v.leftP == "" {
			side = 0
		} else {
			side = 1
		}
	}
	if side >= 0 {
		v.loadSide(side, uris[0])
	}
}

func (v *diffView) buildToolbar(side int) *fyne.Container {
	first := ttwidget.NewButtonWithIcon("", theme.MediaFastRewindIcon(), func() { v.jumpToFileStart() })
	first.SetToolTip("Jump to start of diff")
	prev := ttwidget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { v.jumpDiff(-1) })
	prev.SetToolTip("Previous change (wraps)")
	next := ttwidget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() { v.jumpDiff(1) })
	next.SetToolTip("Next change (wraps)")
	last := ttwidget.NewButtonWithIcon("", theme.MediaFastForwardIcon(), func() { v.jumpToFileEnd() })
	last.SetToolTip("Jump to end of diff")
	for _, b := range []*ttwidget.Button{first, prev, next, last} {
		b.Importance = widget.LowImportance
	}
	var recent *ttwidget.Button
	recent = ttwidget.NewButtonWithIcon("", theme.HistoryIcon(), func() {
		v.showRecentMenuForSide(side, recent)
	})
	recent.SetToolTip("Open a recently used file on this side")
	recent.Importance = widget.LowImportance
	reload := ttwidget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { v.reloadSideAttempt(side) })
	reload.SetToolTip("Reload this file from disk (re-reads from disk; asks before discarding unsaved edits)")
	reload.Importance = widget.LowImportance
	v.btnReloadPane[side] = reload
	open := ttwidget.NewButton("Browse…", func() { v.openFileDialog(side) })
	open.SetToolTip("Choose a file from disk for this side")
	open.Importance = widget.MediumImportance

	top := container.NewHBox(first, prev, next, last, widget.NewSeparator(), reload, recent, widget.NewSeparator(), open)

	entry := widget.NewEntry()
	entry.SetPlaceHolder("Find…")
	entry.OnSubmitted = func(_ string) { v.findInPane(side, true) }
	// HBox would keep the entry at its tiny intrinsic min width; force ~5× that (floor 360px).
	findW := entry.MinSize().Width * 5
	if findW < 360 {
		findW = 360
	}
	findGuide := canvas.NewRectangle(color.Transparent)
	findGuide.SetMinSize(fyne.NewSize(findW, 1))
	entryWide := container.NewMax(findGuide, entry)

	re := ttwidget.NewCheck(".*", nil)
	re.SetToolTip("Search with regular expressions (Go regexp syntax). When off, search is literal text.")
	caseChk := ttwidget.NewCheck("Aa", nil)
	caseChk.SetToolTip("Match case. When off, literal search ignores case; regex search adds (?i) unless the pattern already sets it.")
	findPrev := ttwidget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() { v.findInPane(side, false) })
	findPrev.SetToolTip("Find previous in this pane (wraps)")
	findNext := ttwidget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() { v.findInPane(side, true) })
	findNext.SetToolTip("Find next in this pane (wraps)")
	for _, b := range []*ttwidget.Button{findPrev, findNext} {
		b.Importance = widget.LowImportance
	}
	v.paneSearchEntry[side] = entry
	v.paneSearchRegex[side] = re
	v.paneSearchCase[side] = caseChk

	searchRow := container.NewHBox(entryWide, re, caseChk, findPrev, findNext)
	return container.NewVBox(top, searchRow)
}

func (v *diffView) buildUI() fyne.CanvasObject {
	v.leftTitle = widget.NewLabel("(none)")
	v.rightTitle = widget.NewLabel("(none)")
	v.leftTitle.TextStyle = fyne.TextStyle{Bold: true}
	v.rightTitle.TextStyle = fyne.TextStyle{Bold: true}
	v.leftTitle.Truncation = fyne.TextTruncateEllipsis
	v.rightTitle.Truncation = fyne.TextTruncateEllipsis

	hintL := widget.NewLabel("Drop a file here or use Recent / Browse above, or File menu")
	hintR := widget.NewLabel("Drop a file here or use Recent / Browse above, or File menu")
	hintL.TextStyle = fyne.TextStyle{Italic: true}
	hintR.TextStyle = fyne.TextStyle{Italic: true}

	v.leftList = widget.NewList(
		func() int {
			if v.model == nil {
				return 0
			}
			return len(v.model.Rows)
		},
		v.makeLineCell(0),
		v.updateLineCell(0),
	)
	v.rightList = widget.NewList(
		func() int {
			if v.model == nil {
				return 0
			}
			return len(v.model.Rows)
		},
		v.makeLineCell(1),
		v.updateLineCell(1),
	)

	v.leftList.OnSelected = func(id widget.ListItemID) { v.syncFrom(v.leftList, v.rightList, id) }
	v.rightList.OnSelected = func(id widget.ListItemID) { v.syncFrom(v.rightList, v.leftList, id) }

	leftHead := container.NewVBox(v.buildToolbar(0), v.leftTitle, hintL)
	rightHead := container.NewVBox(v.buildToolbar(1), v.rightTitle, hintR)

	leftScroll := container.NewScroll(v.leftList)
	rightScroll := container.NewScroll(v.rightList)
	leftScroll.SetMinSize(fyne.NewSize(200, 200))
	rightScroll.SetMinSize(fyne.NewSize(200, 200))

	v.leftCol = container.NewBorder(leftHead, nil, nil, nil, leftScroll)
	v.rightCol = container.NewBorder(rightHead, nil, nil, nil, rightScroll)

	split := container.NewHSplit(v.leftCol, v.rightCol)
	split.SetOffset(loadSplitOffset(v.app))
	v.mainSplit = split

	pad := layout.NewCustomPaddedLayout(3, 0, 3, 0)
	titleLbl := widget.NewLabel(appName)
	titleLbl.TextStyle = fyne.TextStyle{Bold: true}
	verLbl := widget.NewLabel("v" + appVersion)
	headerRow := container.NewHBox(
		container.New(pad, newBrandingHeaderImage(resourceKrankyBearNerdPng)),
		container.NewVBox(titleLbl, verLbl),
		layout.NewSpacer(),
	)

	toolRow := v.buildMainChromeToolbar()
	v.recompute()
	v.refreshMainToolbar()
	// VBox would size the split to its minimum height only; Border lets the split
	// consume all space below the header (full-height scroll panes).
	topBar := container.NewVBox(headerRow, toolRow, widget.NewSeparator())
	return container.NewBorder(topBar, nil, nil, nil, split)
}

func (v *diffView) refreshDiffLists() {
	if v.leftList != nil {
		v.leftList.Refresh()
	}
	if v.rightList != nil {
		v.rightList.Refresh()
	}
}

func (v *diffView) registerMainCanvasShortcuts(c fyne.Canvas) {
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyZ, Modifier: fyne.KeyModifierShortcutDefault}, func(fyne.Shortcut) {
		v.undoEdit()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyZ, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}, func(fyne.Shortcut) {
		v.redoEdit()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyC, Modifier: fyne.KeyModifierShortcutDefault}, func(fyne.Shortcut) {
		v.copySelectedRowToClipboard()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyX, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}, func(fyne.Shortcut) {
		v.swapSides()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyComma, Modifier: fyne.KeyModifierAlt}, func(fyne.Shortcut) {
		v.jumpDiff(-1)
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyPeriod, Modifier: fyne.KeyModifierAlt}, func(fyne.Shortcut) {
		v.jumpDiff(1)
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyHome, Modifier: fyne.KeyModifierAlt}, func(fyne.Shortcut) {
		v.jumpToFileStart()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEnd, Modifier: fyne.KeyModifierAlt}, func(fyne.Shortcut) {
		v.jumpToFileEnd()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyE, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}, func(fyne.Shortcut) {
		v.exportUnifiedPatch()
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyU, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}, func(fyne.Shortcut) {
		v.copyUnifiedPatchToClipboard()
	})
}

// openDiffWindow builds the two-pane diff GUI as a window on the given app.
// When master is true, the window owns the app lifecycle (close = quit) and
// installs the system tray. When false, the window is secondary (close = hide)
// and the tray is left alone — the caller (explorer) owns it.
func openDiffWindow(a fyne.App, master bool) *diffView {
	v := &diffView{
		app:             a,
		showLineNumbers: a.Preferences().BoolWithFallback(prefShowLineNumbers, false),
		showWhitespace:  a.Preferences().BoolWithFallback(prefShowWhitespace, false),
		syncScrollOn:    a.Preferences().BoolWithFallback(prefSyncScroll, false),
	}
	title := appName + " — Diff"
	if master {
		title = appName
	}
	w := a.NewWindow(title)
	v.win = w
	w.SetIcon(resourceKrankyBearNerdPng)
	w.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(v.buildUI()), w.Canvas()))
	v.registerMainCanvasShortcuts(w.Canvas())
	w.Resize(mainWindowLaunchSize(a))
	w.SetOnDropped(v.dropTarget)

	if master {
		w.SetMaster()
		w.SetCloseIntercept(func() {
			if v.flyoutPop != nil {
				fynetooltip.DestroyPopUpToolTipLayer(v.flyoutPop)
				v.flyoutPop = nil
			}
			fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
			quitFromMainWindow(v)
		})
	} else {
		w.SetCloseIntercept(func() {
			if v.flyoutPop != nil {
				fynetooltip.DestroyPopUpToolTipLayer(v.flyoutPop)
				v.flyoutPop = nil
			}
			fynetooltip.DestroyWindowToolTipLayer(w.Canvas())
			windowHide(w)
		})
	}

	windowShow(w)
	go v.syncScrollPollLoop()
	v.setupMenus(master)
	return v
}
