package main

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// showPreferences edits appearance and recent-file lists stored in app preferences.
// v may be nil — when the dialog is opened from the explorer there's no diff
// view to live-update, but the persisted preferences still take effect on the
// next-opened diff window, and theme/window-size apply app-wide.
func showPreferences(a fyne.App, v *diffView) {
	var parent fyne.Window
	if v != nil {
		parent = v.win
	}
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

	singleDiff := widget.NewCheck("Keep only one diff window open at a time", nil)
	singleDiff.Checked = a.Preferences().BoolWithFallback(prefSingleDiffWindow, true)
	singleDiffNote := widget.NewLabel("When enabled, opening a new diff (Compare Two Files…, Diff against HEAD, or any file click in Repo History) closes any existing diff window. Windows with unsaved edits are kept open so work is never silently lost. Disable if you want to compare several files side-by-side.")
	singleDiffNote.Wrapping = fyne.TextWrapWord

	showDotGit := widget.NewCheck("Show .git directory in folder listings", nil)
	showDotGit.Checked = a.Preferences().BoolWithFallback(prefShowDotGit, false)
	showDotGitNote := widget.NewLabel("Off by default — .git is usually noise in the worktree view. Use the Tracked files toolbar button to inspect what git is tracking. Turn this on if you actually want to drill into .git's internals.")
	showDotGitNote.Wrapping = fyne.TextWrapWord

	autoRefresh := widget.NewCheck("Auto-refresh when files change outside the app", nil)
	autoRefresh.Checked = a.Preferences().BoolWithFallback(prefAutoRefresh, true)
	autoRefreshNote := widget.NewLabel("On by default. Watches the current folder + .git/index so external edits (your IDE saving a file, a CLI commit, a branch switch) update the explorer without you hitting Refresh. Events are debounced ~250ms to avoid flicker. Turn off if you'd rather refresh manually.")
	autoRefreshNote.Wrapping = fyne.TextWrapWord

	dailyDepAlert := widget.NewCheck("Daily background Dependabot scan", nil)
	dailyDepAlert.Checked = a.Preferences().Bool(prefDailyDepAlertEnabled)
	dailyDepAlertTime := widget.NewEntry()
	dailyDepAlertTime.SetText(a.Preferences().StringWithFallback(prefDailyDepAlertTime, "09:00"))
	dailyDepAlertTime.SetPlaceHolder("HH:MM (24-hour)")
	dailyDepAlertTime.Validator = func(s string) error {
		if _, err := time.Parse("15:04", strings.TrimSpace(s)); err != nil {
			return fmt.Errorf("use 24-hour HH:MM, e.g. 09:00")
		}
		return nil
	}
	dailyDepAlertNote := widget.NewLabel("Once per day at the set time, sweeps your repos for open GitHub Dependabot alerts and updates the 🛡 header badge — silently, no pop-ups. If the machine is asleep at that time, it runs at the next opportunity after waking (still at most once a day). Needs the gh CLI authenticated (gh auth login). Local dep-scan stays on-demand.")
	dailyDepAlertNote.Wrapping = fyne.TextWrapWord

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
			if v != nil {
				v.refreshMainMenu()
			}
		}, parent)
	})
	clearRight := widget.NewButton("Clear right recent files", func() {
		dialog.ShowConfirm("Clear recent files", "Remove all saved paths for the right pane?", func(ok bool) {
			if !ok {
				return
			}
			clearRecent(a, 1)
			if v != nil {
				v.refreshMainMenu()
			}
		}, parent)
	})
	clearAll := widget.NewButton("Clear both sides", func() {
		dialog.ShowConfirm("Clear recent files", "Remove all recent file paths for left and right?", func(ok bool) {
			if !ok {
				return
			}
			clearAllRecent(a)
			if v != nil {
				v.refreshMainMenu()
			}
		}, parent)
	})

	clearFolders := widget.NewButton("Clear recent folders", func() {
		dialog.ShowConfirm("Clear recent folders", "Remove all saved folder paths from the explorer's recent list?", func(ok bool) {
			if !ok {
				return
			}
			clearRecentFolders(a)
			if explorerPrefsChangedHook != nil {
				explorerPrefsChangedHook()
			}
		}, parent)
	})

	recentsIntro := widget.NewLabel("Recent paths are updated when you open a file. Open them again from File → Open Recent, or the history icon in each pane’s toolbar (the tray has no recent list).")
	recentsIntro.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		widget.NewLabelWithStyle("Appearance", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		themeRadio,
		appearanceNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Explorer", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		showDotGit,
		showDotGitNote,
		autoRefresh,
		autoRefreshNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Automatic scanning", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		dailyDepAlert,
		container.NewBorder(nil, nil, widget.NewLabel("Time"), nil, dailyDepAlertTime),
		dailyDepAlertNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Diff view", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		showLineNums,
		lineNumsNote,
		showWS,
		wsNote,
		syncScroll,
		syncScrollNote,
		singleDiff,
		singleDiffNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Window", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		rememberWin,
		rememberWinNote,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Recent files", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		recentsIntro,
		container.NewGridWithColumns(2, clearLeft, clearRight),
		clearAll,
		clearFolders,
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
		// Persist the three diff-view defaults regardless of whether a diff view
		// is open right now — the next-opened diff window reads these as its
		// initial state.
		a.Preferences().SetBool(prefShowLineNumbers, showLineNums.Checked)
		a.Preferences().SetBool(prefShowWhitespace, showWS.Checked)
		a.Preferences().SetBool(prefSyncScroll, syncScroll.Checked)
		a.Preferences().SetBool(prefSingleDiffWindow, singleDiff.Checked)
		a.Preferences().SetBool(prefShowDotGit, showDotGit.Checked)
		a.Preferences().SetBool(prefAutoRefresh, autoRefresh.Checked)
		a.Preferences().SetBool(prefDailyDepAlertEnabled, dailyDepAlert.Checked)
		// Persist the time only when valid; keep the prior value otherwise so a
		// typo doesn't disable an already-configured schedule.
		if _, err := time.Parse("15:04", strings.TrimSpace(dailyDepAlertTime.Text)); err == nil {
			a.Preferences().SetString(prefDailyDepAlertTime, strings.TrimSpace(dailyDepAlertTime.Text))
		}
		a.Preferences().SetBool(prefRememberWindowSize, rememberWin.Checked)
		// Ask the explorer to re-apply preferences (refresh the menu + the
		// worktree listing in case "Show .git" flipped).
		if explorerPrefsChangedHook != nil {
			explorerPrefsChangedHook()
		}
		if rememberWin.Checked && parent != nil {
			saveMainWindowGeometryIfEnabled(a, parent)
		}
		if v != nil {
			v.showLineNumbers = showLineNums.Checked
			v.showWhitespace = showWS.Checked
			v.syncScrollOn = syncScroll.Checked
			if v.syncScrollOn && v.leftList != nil && v.rightList != nil {
				v.syncScrollPrevL = v.leftList.GetScrollOffset()
				v.syncScrollPrevR = v.rightList.GetScrollOffset()
			}
			v.refreshDiffLists()
			v.refreshMainToolbar()
			v.refreshMainMenu()
		}
	}, parent)
	d.Resize(fyne.NewSize(520, 620))
	d.Show()
}
