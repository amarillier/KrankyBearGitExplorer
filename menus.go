package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func undoMenuItem(v *diffView) *fyne.MenuItem {
	it := fyne.NewMenuItem("Undo", func() { v.undoEdit() })
	it.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyZ, Modifier: fyne.KeyModifierShortcutDefault}
	it.Disabled = len(v.undoStack) == 0
	return it
}

func redoMenuItem(v *diffView) *fyne.MenuItem {
	it := fyne.NewMenuItem("Redo", func() { v.redoEdit() })
	it.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyZ, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}
	it.Disabled = len(v.redoStack) == 0
	return it
}

func lineNumsMenuItem(v *diffView) *fyne.MenuItem {
	it := fyne.NewMenuItem("Line Numbers", func() {
		v.showLineNumbers = !v.showLineNumbers
		v.app.Preferences().SetBool(prefShowLineNumbers, v.showLineNumbers)
		v.refreshDiffLists()
		v.refreshMainToolbar()
		v.refreshMainMenu()
	})
	it.Checked = v.showLineNumbers
	return it
}

func showWhitespaceMenuItem(v *diffView) *fyne.MenuItem {
	it := fyne.NewMenuItem("Show Whitespace", func() {
		v.showWhitespace = !v.showWhitespace
		v.app.Preferences().SetBool(prefShowWhitespace, v.showWhitespace)
		v.refreshDiffLists()
		v.refreshMainToolbar()
		v.refreshMainMenu()
	})
	it.Checked = v.showWhitespace
	return it
}

func syncScrollMenuItem(v *diffView) *fyne.MenuItem {
	it := fyne.NewMenuItem("Sync scroll", func() {
		v.syncScrollOn = !v.syncScrollOn
		if v.syncScrollOn && v.leftList != nil && v.rightList != nil {
			v.syncScrollPrevL = v.leftList.GetScrollOffset()
			v.syncScrollPrevR = v.rightList.GetScrollOffset()
		}
		v.app.Preferences().SetBool(prefSyncScroll, v.syncScrollOn)
		v.refreshMainToolbar()
		v.refreshMainMenu()
	})
	it.Checked = v.syncScrollOn
	return it
}

// hideAllAppWindows hides every window the app considers visible right now,
// and snapshots that set so bringAllAppWindowsToFront can restore exactly the
// same windows. Already-dismissed dialogs are skipped — they stay dismissed
// through a Hide-All / Show-All round trip.
func hideAllAppWindows(a fyne.App) {
	hiddenByHideAll = hiddenByHideAll[:0]
	for _, win := range a.Driver().AllWindows() {
		if win == nil {
			continue
		}
		if !isWindowConsideredVisible(win) {
			continue
		}
		hiddenByHideAll = append(hiddenByHideAll, win)
		windowHide(win)
	}
}

// bringAllAppWindowsToFront restores the exact set of windows hideAllAppWindows
// last hid (if any). If there's no snapshot — i.e. the user didn't just click
// Hide All — it falls back to bringing the main window forward, since that's
// the natural meaning of "show me my app again".
func bringAllAppWindowsToFront(_ fyne.App, mainW fyne.Window) {
	if len(hiddenByHideAll) > 0 {
		for _, win := range hiddenByHideAll {
			windowShow(win)
		}
		hiddenByHideAll = hiddenByHideAll[:0]
		if mainW != nil {
			mainW.RequestFocus()
		}
		return
	}
	if mainW != nil {
		windowShow(mainW)
		mainW.RequestFocus()
	}
}

func (v *diffView) buildRecentSubmenu(side int) *fyne.Menu {
	paths := loadRecentList(v.app, recentKey(side))
	if len(paths) == 0 {
		empty := fyne.NewMenuItem("(no recent files)", nil)
		empty.Disabled = true
		return fyne.NewMenu("", empty)
	}
	items := make([]*fyne.MenuItem, 0, len(paths))
	for _, p := range paths {
		path := p
		items = append(items, fyne.NewMenuItem(recentMenuLabel(path), func() {
			v.loadPathFromRecent(side, path)
		}))
	}
	return fyne.NewMenu("", items...)
}

func (v *diffView) buildMainMenu() *fyne.MainMenu {
	recentLeft := fyne.NewMenuItem("Left file", nil)
	recentLeft.ChildMenu = v.buildRecentSubmenu(0)
	recentRight := fyne.NewMenuItem("Right file", nil)
	recentRight.ChildMenu = v.buildRecentSubmenu(1)
	openRecent := fyne.NewMenuItem("Open Recent", nil)
	openRecent.ChildMenu = fyne.NewMenu("", recentLeft, recentRight)

	saveLeft := fyne.NewMenuItem("Save Left File", func() { v.saveSideAttempt(0) })
	saveLeft.Disabled = v.leftP == "" || !v.leftDirty
	saveRight := fyne.NewMenuItem("Save Right File", func() { v.saveSideAttempt(1) })
	saveRight.Disabled = v.rightP == "" || !v.rightDirty
	saveBoth := fyne.NewMenuItem("Save Both Files", func() { v.saveBothAttempt() })
	saveBoth.Disabled = (v.leftP == "" || !v.leftDirty) && (v.rightP == "" || !v.rightDirty)

	exportPatch := fyne.NewMenuItem("Export unified patch…", func() { v.exportUnifiedPatch() })
	exportPatch.Disabled = v.leftT == "" && v.rightT == "" && v.leftP == "" && v.rightP == ""
	exportPatch.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyE, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}

	file := fyne.NewMenu("File",
		fyne.NewMenuItem("Open Left File…", func() { v.openFileDialog(0) }),
		fyne.NewMenuItem("Open Right File…", func() { v.openFileDialog(1) }),
		openRecent,
		fyne.NewMenuItemSeparator(),
		saveLeft,
		saveRight,
		saveBoth,
		fyne.NewMenuItemSeparator(),
		exportPatch,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Preferences…", func() { showPreferences(v.app, v) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { quitFromMainWindow(v) }),
	)

	copyUnifiedPatch := fyne.NewMenuItem("Copy unified patch", func() { v.copyUnifiedPatchToClipboard() })
	copyUnifiedPatch.Disabled = v.leftT == "" && v.rightT == "" && v.leftP == "" && v.rightP == ""
	copyUnifiedPatch.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyU, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}

	copyLeft := fyne.NewMenuItem("Copy left line", func() { v.copySelectedLeftLine() })
	copyRight := fyne.NewMenuItem("Copy right line", func() { v.copySelectedRightLine() })
	copyAligned := fyne.NewMenuItem("Copy aligned row", func() { v.copySelectedRowToClipboard() })
	copyAligned.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyC, Modifier: fyne.KeyModifierShortcutDefault}
	selOK := v.hasDiffSelection && v.model != nil && v.selectedDiffRow >= 0 && v.selectedDiffRow < len(v.model.Rows)
	var dr DiffRow
	if selOK {
		dr = v.model.Rows[v.selectedDiffRow]
	}
	rowCopyOK := selOK && (dr.LeftLineNo > 0 || dr.RightLineNo > 0)
	copyLeft.Disabled = !selOK || dr.LeftLineNo <= 0
	copyRight.Disabled = !selOK || dr.RightLineNo <= 0
	copyAligned.Disabled = !rowCopyOK

	applyLRLabel := "Apply left to right"
	applyRLLabel := "Apply right to left"
	if selOK {
		applyLRLabel = applyLeftToRightMenuLabel(dr)
		applyRLLabel = applyRightToLeftMenuLabel(dr)
	}
	applyLR := fyne.NewMenuItem(applyLRLabel, func() {
		if !v.hasDiffSelection || v.model == nil {
			return
		}
		rid := v.selectedDiffRow
		if rid < 0 || int(rid) >= len(v.model.Rows) {
			return
		}
		v.runApplyLeftToRightAtRow(rid)
	})
	applyLR.Disabled = !selOK || !canApplyLeftToRightAtRow(v.model, v.selectedDiffRow)
	applyRL := fyne.NewMenuItem(applyRLLabel, func() {
		if !v.hasDiffSelection || v.model == nil {
			return
		}
		rid := v.selectedDiffRow
		if rid < 0 || int(rid) >= len(v.model.Rows) {
			return
		}
		v.runApplyRightToLeftAtRow(rid)
	})
	applyRL.Disabled = !selOK || !canApplyRightToLeftAtRow(v.model, v.selectedDiffRow)

	delLeftMain := fyne.NewMenuItem("Delete line from left file", func() {
		if !v.hasDiffSelection || v.model == nil {
			return
		}
		rid := v.selectedDiffRow
		if rid < 0 || int(rid) >= len(v.model.Rows) {
			return
		}
		v.runDeleteLeftAtRow(rid)
	})
	delLeftMain.Disabled = !selOK || dr.LeftLineNo <= 0
	delRightMain := fyne.NewMenuItem("Delete line from right file", func() {
		if !v.hasDiffSelection || v.model == nil {
			return
		}
		rid := v.selectedDiffRow
		if rid < 0 || int(rid) >= len(v.model.Rows) {
			return
		}
		v.runDeleteRightAtRow(rid)
	})
	delRightMain.Disabled = !selOK || dr.RightLineNo <= 0

	includeDelLeft := true
	includeDelRight := true
	if selOK {
		if contextDeleteLeftDuplicatesApplyRightToLeft(dr) {
			includeDelLeft = false
		}
		if contextDeleteRightDuplicatesApplyLeftToRight(dr) {
			includeDelRight = false
		}
	}
	editItems := []*fyne.MenuItem{
		undoMenuItem(v),
		redoMenuItem(v),
		fyne.NewMenuItemSeparator(),
		copyLeft,
		copyRight,
		copyAligned,
		copyUnifiedPatch,
		fyne.NewMenuItemSeparator(),
		applyLR,
		applyRL,
	}
	if includeDelLeft || includeDelRight {
		editItems = append(editItems, fyne.NewMenuItemSeparator())
		if includeDelLeft {
			editItems = append(editItems, delLeftMain)
		}
		if includeDelRight {
			editItems = append(editItems, delRightMain)
		}
	}
	edit := fyne.NewMenu("Edit", editItems...)

	noChanges := v.model == nil || len(v.model.ChangeIndices) == 0
	emptyDiff := v.model == nil || len(v.model.Rows) == 0
	prevChange := fyne.NewMenuItem("Previous change", func() { v.jumpDiff(-1) })
	prevChange.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyComma, Modifier: fyne.KeyModifierAlt}
	prevChange.Disabled = noChanges
	nextChange := fyne.NewMenuItem("Next change", func() { v.jumpDiff(1) })
	nextChange.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyPeriod, Modifier: fyne.KeyModifierAlt}
	nextChange.Disabled = noChanges
	jumpStart := fyne.NewMenuItem("Jump to start of diff", func() { v.jumpToFileStart() })
	jumpStart.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyHome, Modifier: fyne.KeyModifierAlt}
	jumpStart.Disabled = emptyDiff
	jumpEnd := fyne.NewMenuItem("Jump to end of diff", func() { v.jumpToFileEnd() })
	jumpEnd.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyEnd, Modifier: fyne.KeyModifierAlt}
	jumpEnd.Disabled = emptyDiff
	swapSides := fyne.NewMenuItem("Swap left and right", func() { v.swapSides() })
	swapSides.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyX, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}

	view := fyne.NewMenu("View",
		fyne.NewMenuItem("Show All Windows", func() { bringAllAppWindowsToFront(v.app, v.win) }),
		fyne.NewMenuItem("Hide All Windows", func() { hideAllAppWindows(v.app) }),
		fyne.NewMenuItemSeparator(),
		prevChange,
		nextChange,
		jumpStart,
		jumpEnd,
		swapSides,
		fyne.NewMenuItemSeparator(),
		lineNumsMenuItem(v),
		showWhitespaceMenuItem(v),
		syncScrollMenuItem(v),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Light Theme", func() {
			setLightTheme(v.app)
			v.refreshDiffLists()
		}),
		fyne.NewMenuItem("Dark Theme", func() {
			setDarkTheme(v.app)
			v.refreshDiffLists()
		}),
		fyne.NewMenuItem("System Theme", func() {
			setSystemTheme(v.app)
			v.refreshDiffLists()
		}),
	)

	help := fyne.NewMenu("Help",
		fyne.NewMenuItem("Help", func() { showHelp(v.app) }),
		fyne.NewMenuItem("About", func() { showAbout(v.app) }),
		fyne.NewMenuItem("Check for Updates…", func() { checkForUpdates(v.app) }),
	)

	return fyne.NewMainMenu(file, edit, view, help)
}

// buildTrayMenu returns the system tray menu. It must stay shallow (no nested
// submenus that change after launch): each SetSystemTrayMenu call on the GLFW
// driver spawns new goroutines per item without tearing down old listeners,
// so rebuilding the tray repeatedly leads to leaks and native crashes on macOS.
func (v *diffView) buildTrayMenu() *fyne.Menu {
	trayRecentHint := fyne.NewMenuItem("Recent files: use menu bar → File → Open Recent", nil)
	trayRecentHint.Disabled = true

	// Tray menu is built only once (see comment below); keep this item always enabled so it stays
	// useful after files are opened. exportUnifiedPatch shows a dialog if nothing is loaded.
	trayExportPatch := fyne.NewMenuItem("Export unified patch…", func() { v.exportUnifiedPatch() })

	return fyne.NewMenu(appName,
		fyne.NewMenuItem("Show All Windows", func() { bringAllAppWindowsToFront(v.app, v.win) }),
		fyne.NewMenuItem("Hide All Windows", func() { hideAllAppWindows(v.app) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Open Left File…", func() { v.openFileDialog(0) }),
		fyne.NewMenuItem("Open Right File…", func() { v.openFileDialog(1) }),
		trayExportPatch,
		trayRecentHint,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Preferences…", func() { showPreferences(v.app, v) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Light Theme", func() {
			setLightTheme(v.app)
			v.refreshDiffLists()
		}),
		fyne.NewMenuItem("Dark Theme", func() {
			setDarkTheme(v.app)
			v.refreshDiffLists()
		}),
		fyne.NewMenuItem("System Theme", func() {
			setSystemTheme(v.app)
			v.refreshDiffLists()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Help", func() { showHelp(v.app) }),
		fyne.NewMenuItem("About", func() { showAbout(v.app) }),
		fyne.NewMenuItem("Check for Updates…", func() { checkForUpdates(v.app) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { quitFromMainWindow(v) }),
	)
}

// refreshMainMenu rebuilds only the window menu bar (safe to call often).
func (v *diffView) refreshMainMenu() {
	fyne.Do(func() {
		if v.win == nil {
			return
		}
		v.win.SetMainMenu(v.buildMainMenu())
	})
}

// setupMenus installs the menu bar and (when setTray is true) the system tray
// once after the main window is shown. The explorer view owns the tray when
// the diff window is opened as a secondary window, so it passes false here.
func (v *diffView) setupMenus(setTray bool) {
	fyne.Do(func() {
		if v.win == nil {
			return
		}
		v.win.SetMainMenu(v.buildMainMenu())
		if setTray {
			if desk, ok := v.app.(desktop.App); ok {
				desk.SetSystemTrayMenu(v.buildTrayMenu())
				desk.SetSystemTrayIcon(resourceKrankyBearNerdPng)
			}
		}
	})
}
