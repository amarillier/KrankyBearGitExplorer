# KrankyBear GitExplorer

A cross-platform GUI git repository explorer built with Go and the Fyne GUI toolkit.

The goal isn't to replace the git CLI or your IDE's git integration — those still own the day-to-day commit / push / branch / rebase workflow. KrankyBearGitExplorer is for the cases where it's easier to *look* than to type: drop a project folder in, see what's tracked, what's modified, what's untracked, on which branch, with what counts. Click `.git` to see the tracked-files tree. Right-click a row for selective edits — reveal, copy path, open in your editor, diff against HEAD, or remove from git.

The two-pane side-by-side diff tool this app grew out of is preserved as a secondary window (File → Compare Two Files…) and is also wired into the new "Diff against HEAD" workflow.

## Features

### Folder view
- Open a folder by file picker, drag-and-drop, or keyboard (Cmd/Ctrl+O).
- Per-entry **Name · Size · Modified · Status** columns. Directories first, alphabetical.
- The `.git` directory is pinned at the top with an inline hint — click it to switch into the tracked-files tree.
- Repo header shows current path, branch name, and clean / dirty+untracked file counts when inside a repo.

### Tracked-files view
- Tree of everything git is tracking inside the repo, expandable by directory.
- Non-clean leaves are annotated with a human-readable status (`modified`, `staged`, `untracked`, `ignored`, etc.) — full mapping via View → Git Status Legend… or the tooltip on the Status column header.
- Up arrow returns to the folder view of the same repo (one logical level back), not the filesystem parent.

### Row actions (right-click)
Right-click any file or directory row in the folder view to act on it:

- **Reveal in Finder / Explorer / file manager** — cross-platform; uses `open -R` on macOS, `explorer /select` on Windows, `xdg-open` on Linux.
- **Copy path** — absolute path to the clipboard.
- **Open in `$EDITOR`** — honours `$VISUAL` then `$EDITOR` (supports `"code --wait"`-style commands with flags); falls back to the OS default open command.
- **Diff against HEAD…** — see below.
- **git rm -f** — remove from worktree + index, with a confirm dialog (uses go-git's `wt.Remove`).
- **git rm --cached** — remove from index only, keep the file on disk; pairs with manually adding a `.gitignore` entry.

Items are enabled or disabled per row based on whether the entry is tracked, untracked, ignored, a directory, or `.git`.

### Diff against HEAD
Right-click a tracked file → **Diff against HEAD…** to open the existing two-pane diff with:

- **Left** = the HEAD blob, labelled `HEAD@<short-sha>: <relative-path>  (read-only)`, marked read-only.
- **Right** = the current worktree file, fully editable.

Edits flow only from HEAD → worktree (via the existing "Apply left to right" merge action). The reverse direction is blocked so you can't accidentally mutate the in-memory HEAD buffer. Read-only enforcement is symmetric — Swap left/right keeps the HEAD content protected on whichever side it now occupies.

### Compare Two Files… (legacy diff tool, preserved)
- Side-by-side diff with sync-scroll, line-numbers toggle, whitespace visualization.
- Row-level merge actions (apply left→right, apply right→left, delete row).
- Unified-patch export (file + clipboard).
- Undo / redo on merge edits.
- Reachable from the explorer's File menu and the system tray.

### Preferences
File → Preferences… (from either window) opens a single dialog:

- Theme (Light / Dark / System) — applies app-wide, persisted.
- Diff-view defaults: line numbers, visible whitespace, sync scroll — take effect on the next-opened diff window.
- Remember main window size between launches.
- Clear recent files (left / right / both).

### Window management
- **Hide All Windows** snapshots which windows are currently visible and hides exactly those.
- **Show All Windows** restores exactly that snapshot — dialogs you'd already dismissed stay dismissed.

### Cross-platform
- macOS 10.13+, Windows 10+, Linux (X11 or Wayland; tested on GNOME, KDE, XFCE, Cinnamon, MATE).
- System tray support where the OS permits.

## Status

This is an active rebuild on top of [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff). The diff engine is intact and reachable from the File menu or the new "Diff against HEAD" action. Current release: **v0.2.0** — see [ReleaseNotes.txt](ReleaseNotes.txt) for what landed.

### Known gaps as of v0.2.0
- Directory rows don't roll up the status of their children (a folder containing a modified file shows blank in Status).
- No "Recent folders" list yet — open a folder each session.
- No auto-refresh — click Refresh (Cmd/Ctrl+R) or use the toolbar reload after external changes.
- `git rm --cached` shells out to the `git` CLI (`git -C <repo> rm --cached`); requires `git` on `PATH`.
- The Diff against HEAD view is read-only on the HEAD side but only at the data-mutation level — the per-pane Browse / Recent / Reload buttons on the HEAD side are present but no-op; a future polish pass will hide them.

## Dependencies

- [Fyne](https://fyne.io/) v2.7.x — cross-platform Go GUI toolkit (requires OpenGL).
- [go-git/v5](https://github.com/go-git/go-git) — pure-Go git library, used for status / tracked-files / branch info.
- [fyne-tooltip](https://github.com/dweymouth/fyne-tooltip) — tooltip support (vendored).
- Go standard library.

## Building

See the `compile-*.sh` scripts for per-platform builds and `package.sh` for installers. `go build ./...` produces a local binary.

## License

GNU GPL-3.0. See [LICENSE](LICENSE).

## Contributing

Issues and pull requests welcome — small, focused changes please. Design philosophy: align with Fyne's idioms, prefer functionality and correctness over cleverness, keep the dep set tight.

## Author

Allan Marillier

## Acknowledgments

- Built with [Fyne](https://fyne.io/).
- Built on top of [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff), whose two-pane diff engine survives here as the Compare Two Files… view.
