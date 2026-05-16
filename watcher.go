package main

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// repoWatcher wraps an fsnotify.Watcher with a debounced callback so a single
// user action (e.g. saving a file in $EDITOR, which fires several rapid
// CREATE/RENAME/WRITE events for the swap + temp + final files) results in
// one refresh, not a flurry of them.
//
// Watched targets, when the watcher is started:
//   - the current folder (`currentPath`) — catches edits to visible files
//     and new/removed entries
//   - `.git/index` inside the repo (when in a repo) — catches stage / commit
//     / branch-switch operations that don't touch the worktree's listing
//     but do change the per-file status the explorer displays
//
// Recursive watching is deliberately avoided: on large repos it would add
// thousands of inotify/kqueue handles and we'd still need to debounce the
// resulting noise. The Cmd/Ctrl+R refresh remains for anything deeper than
// the currently-shown folder.
type repoWatcher struct {
	w        *fsnotify.Watcher
	done     chan struct{}
	timer    *time.Timer
	timerMu  sync.Mutex
	onChange func()
}

// startRepoWatcher creates a watcher on the given targets and dispatches
// onChange (already wrapped onto the UI thread by the caller) after a
// ~250ms debounce since the last filesystem event. Returns nil if no
// targets are watchable (e.g. the path was deleted between calls).
func startRepoWatcher(targets []string, onChange func()) *repoWatcher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	added := 0
	for _, t := range targets {
		if t == "" {
			continue
		}
		if err := w.Add(t); err == nil {
			added++
		}
	}
	if added == 0 {
		_ = w.Close()
		return nil
	}
	rw := &repoWatcher{
		w:        w,
		done:     make(chan struct{}),
		onChange: onChange,
	}
	go rw.loop()
	return rw
}

func (rw *repoWatcher) loop() {
	for {
		select {
		case <-rw.done:
			return
		case _, ok := <-rw.w.Events:
			if !ok {
				return
			}
			rw.schedule()
		case _, ok := <-rw.w.Errors:
			if !ok {
				return
			}
			// Drop errors silently — a temp-file rename between event
			// processing isn't worth alerting on.
		}
	}
}

// schedule (re)starts the debounce timer. The 250ms window is empirically
// long enough to coalesce $EDITOR-style save sequences without feeling laggy
// to the user when they're, say, switching branches in the CLI and tabbing
// back to the GUI.
func (rw *repoWatcher) schedule() {
	rw.timerMu.Lock()
	defer rw.timerMu.Unlock()
	if rw.timer != nil {
		rw.timer.Stop()
	}
	rw.timer = time.AfterFunc(250*time.Millisecond, func() {
		if rw.onChange != nil {
			rw.onChange()
		}
	})
}

// stop tears down the watcher and cancels any pending debounce timer. Safe
// to call multiple times.
func (rw *repoWatcher) stop() {
	if rw == nil {
		return
	}
	select {
	case <-rw.done:
		return
	default:
		close(rw.done)
	}
	rw.timerMu.Lock()
	if rw.timer != nil {
		rw.timer.Stop()
		rw.timer = nil
	}
	rw.timerMu.Unlock()
	if rw.w != nil {
		_ = rw.w.Close()
	}
}

// repoWatchTargets builds the list of paths the watcher should subscribe to
// for the given folder + repo root. Caller is responsible for handing this
// to startRepoWatcher.
func repoWatchTargets(currentPath, repoRoot string) []string {
	out := []string{currentPath}
	if repoRoot != "" {
		// .git/index is the canonical "something staged or committed
		// happened" trigger. .git itself can be a gitfile (submodule
		// case); guard with a stat in the caller if you care, fsnotify
		// just no-ops on a missing path.
		out = append(out, filepath.Join(repoRoot, ".git", "index"))
	}
	return out
}
