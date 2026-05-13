package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// showPreferences edits appearance and recent-file lists stored in app preferences.
func showPreferences(a fyne.App, v *diffView) {
	parent := v.win
	if parent == nil && len(a.Driver().AllWindows()) > 0 {
		parent = a.Driver().AllWindows()[0]
	}

	themeRadio := widget.NewRadioGroup([]string{"Light", "Dark", "System"}, nil)
	switch a.Preferences().StringWithFallback("theme", "system") {
	case "light":
		themeRadio.Selected = "Light"
	case "dark":
		themeRadio.Selected = "Dark"
	default:
		themeRadio.Selected = "System"
	}

	appearanceNote := widget.NewLabel("Theme and the options below are saved when you click Save.")
	appearanceNote.Wrapping = fyne.TextWrapWord

	showLineNums := widget.NewCheck("Show line numbers in diff panes", nil)
	showLineNums.Checked = a.Preferences().BoolWithFallback(prefShowLineNumbers, false)
	lineNumsNote := widget.NewLabel("Blank rows with a middle dot (·) in the gutter are alignment placeholders: one side has a real line there and the other side is padded so both panes stay in sync.")
	lineNumsNote.Wrapping = fyne.TextWrapWord

	showWS := widget.NewCheck("Show whitespace (· = space, → = tab per character)", nil)
	showWS.Checked = a.Preferences().BoolWithFallback(prefShowWhitespace, false)
	wsNote := widget.NewLabel("When off, tabs are shown as four spaces for column alignment. When on, each real space and tab in the file is shown as those symbols so spacing differences are obvious.")
	wsNote.Wrapping = fyne.TextWrapWord

	syncScroll := widget.NewCheck("Sync scroll (keep left and right panes at the same vertical offset when scrolling)", nil)
	syncScroll.Checked = a.Preferences().BoolWithFallback(prefSyncScroll, false)
	syncScrollNote := widget.NewLabel("Matches the main toolbar sync-scroll control; both are saved here when you click Save.")
	syncScrollNote.Wrapping = fyne.TextWrapWord

	rememberWin := widget.NewCheck("Remember main window size between launches", nil)
	rememberWin.Checked = a.Preferences().BoolWithFallback(prefRememberWindowSize, false)
	rememberWinNote := widget.NewLabel("When enabled, the window size is stored when you quit the app (menu Quit, tray Quit, or closing the main window). When disabled, the app opens at the default size.")
	rememberWinNote.Wrapping = fyne.TextWrapWord

	clearLeft := widget.NewButton("Clear left recent files", func() {
		dialog.ShowConfirm("Clear recent files", "Remove all saved paths for the left pane?", func(ok bool) {
			if !ok {
				return
			}
			clearRecent(a, 0)
			v.refreshMainMenu()
		}, parent)
	})
	clearRight := widget.NewButton("Clear right recent files", func() {
		dialog.ShowConfirm("Clear recent files", "Remove all saved paths for the right pane?", func(ok bool) {
			if !ok {
				return
			}
			clearRecent(a, 1)
			v.refreshMainMenu()
		}, parent)
	})
	clearAll := widget.NewButton("Clear both sides", func() {
		dialog.ShowConfirm("Clear recent files", "Remove all recent file paths for left and right?", func(ok bool) {
			if !ok {
				return
			}
			clearAllRecent(a)
			v.refreshMainMenu()
		}, parent)
	})

	recentsIntro := widget.NewLabel("Recent paths are updated when you open a file. Open them again from File → Open Recent, or the history icon in each pane’s toolbar (the tray has no recent list).")
	recentsIntro.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		widget.NewLabelWithStyle("Appearance", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		themeRadio,
		appearanceNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Diff view", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		showLineNums,
		lineNumsNote,
		showWS,
		wsNote,
		syncScroll,
		syncScrollNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Window", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		rememberWin,
		rememberWinNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Recent files", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		recentsIntro,
		container.NewGridWithColumns(2, clearLeft, clearRight),
		clearAll,
	)

	d := dialog.NewCustomConfirm("Preferences", "Save", "Cancel", content, func(save bool) {
		if !save {
			return
		}
		switch themeRadio.Selected {
		case "Light":
			setLightTheme(a)
		case "Dark":
			setDarkTheme(a)
		default:
			setSystemTheme(a)
		}
		v.showLineNumbers = showLineNums.Checked
		a.Preferences().SetBool(prefShowLineNumbers, v.showLineNumbers)
		v.showWhitespace = showWS.Checked
		a.Preferences().SetBool(prefShowWhitespace, v.showWhitespace)
		v.syncScrollOn = syncScroll.Checked
		a.Preferences().SetBool(prefSyncScroll, v.syncScrollOn)
		if v.syncScrollOn && v.leftList != nil && v.rightList != nil {
			v.syncScrollPrevL = v.leftList.GetScrollOffset()
			v.syncScrollPrevR = v.rightList.GetScrollOffset()
		}
		a.Preferences().SetBool(prefRememberWindowSize, rememberWin.Checked)
		if rememberWin.Checked && v.win != nil {
			saveMainWindowGeometryIfEnabled(a, v.win)
		}
		v.refreshDiffLists()
		v.refreshMainToolbar()
		v.refreshMainMenu()
	}, parent)
	d.Resize(fyne.NewSize(520, 620))
	d.Show()
}
