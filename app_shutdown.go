package main

import "fyne.io/fyne/v2"

// managedWindowVisible is the source of truth for whether a Fyne window the
// app explicitly Shows/Hides is currently visible. Fyne does not expose a
// per-window IsVisible, so we track it ourselves to make Hide All / Show All
// symmetric (only re-show what was visible at the moment of Hide All).
// Windows not in the map default to "visible" — safe for any caller that has
// not adopted windowShow/windowHide yet.
var managedWindowVisible = map[fyne.Window]bool{}

// hiddenByHideAll snapshots the windows that "Hide All Windows" hid, so that
// "Show All Windows" restores exactly that set — not, e.g., a dialog the user
// had already dismissed before Hide All ran.
var hiddenByHideAll []fyne.Window

func windowShow(w fyne.Window) {
	if w == nil {
		return
	}
	managedWindowVisible[w] = true
	w.Show()
}

func windowHide(w fyne.Window) {
	if w == nil {
		return
	}
	managedWindowVisible[w] = false
	w.Hide()
}

func isWindowConsideredVisible(w fyne.Window) bool {
	if w == nil {
		return false
	}
	if v, ok := managedWindowVisible[w]; ok {
		return v
	}
	return true
}

// hideAuxiliaryWindows hides About, Help, and Update dialogs so they do not
// outlive the main window during shutdown.
func hideAuxiliaryWindows() {
	if aboutWindow != nil {
		windowHide(aboutWindow)
	}
	if helpWindow != nil {
		windowHide(helpWindow)
	}
	if updateWindow != nil {
		windowHide(updateWindow)
	}
}

// quitFromMainWindow performs a full application exit: auxiliary windows,
// GLFW windows, and the system tray (via driver Quit) are torn down.
func quitFromMainWindow(v *diffView) {
	if v == nil {
		return
	}
	saveMainWindowGeometryIfEnabled(v.app, v.win)
	if v.mainSplit != nil {
		v.app.Preferences().SetFloat(prefSplitOffset, v.mainSplit.Offset)
	}
	hideAuxiliaryWindows()
	v.app.Quit()
}
