package main

import "fyne.io/fyne/v2"

// repoChildWindows tracks secondary windows whose content is tied to a
// specific repository — Repo History windows plus repo-bound diff windows
// (Diff vs HEAD, Historical Diff). The legacy Compare Two Files… window is
// deliberately excluded since it's repo-independent.
//
// Used by the explorer's loadFolder() to warn the user when they're about
// to switch the explorer to a different repo while these windows still show
// data from the previous one.
var repoChildWindows []fyne.Window

func registerRepoChildWindow(w fyne.Window) {
	if w == nil {
		return
	}
	repoChildWindows = append(repoChildWindows, w)
}

func unregisterRepoChildWindow(w fyne.Window) {
	if w == nil {
		return
	}
	out := repoChildWindows[:0]
	for _, x := range repoChildWindows {
		if x != w {
			out = append(out, x)
		}
	}
	repoChildWindows = out
}

// closeAllRepoChildWindows fires Close() on every registered window. Each
// window's own close intercept handles teardown (tooltip layer, timers, etc.)
// and unregisters itself — the snapshot guards against the slice mutating
// underneath us as those intercepts run.
func closeAllRepoChildWindows() {
	snapshot := append([]fyne.Window(nil), repoChildWindows...)
	for _, w := range snapshot {
		if w != nil {
			w.Close()
		}
	}
}
