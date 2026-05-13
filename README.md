# KrankyBear GitExplorer

A cross-platform GUI git repository explorer built with Go and the Fyne GUI toolkit.

The goal isn't to replace the git CLI or your IDE's git integration — those still own the day-to-day commit / push / branch / rebase workflow. KrankyBearGitExplorer is for the cases where it's easier to *look* than to type: drop a project folder in, see what's tracked, what's modified, what's untracked, on which branch, with what counts. Click `.git` to see the tracked-files tree. Selective edits (remove from worktree, remove from index only, diff against HEAD) are on the way.

The two-pane side-by-side diff tool this app grew out of is preserved as a secondary window (File → Compare Two Files…) and is earmarked for a future "current file on disk vs. last-committed equivalent" comparison.

## Features

### Folder view
- Open a folder by file picker, drag-and-drop, or keyboard (Cmd/Ctrl+O).
- Per-entry **Name · Size · Modified · Status** columns. Directories first, alphabetical.
- The `.git` directory is pinned at the top with an inline hint — click it to switch into the tracked-files tree.
- Repo header shows current path, branch name, and clean / dirty+untracked file counts when inside a repo.

### Tracked-files view
- Tree of everything git is tracking inside the repo, expandable by directory.
- Non-clean leaves are annotated with a human-readable status (`modified`, `staged`, `untracked`, `ignored`, etc.) — full mapping via View → Git Status Legend… or the tooltip on the Status column header.

### Compare Two Files… (legacy diff tool, preserved)
- Side-by-side diff with sync-scroll, line-numbers toggle, whitespace visualization.
- Row-level merge actions (apply left→right, apply right→left, delete row).
- Unified-patch export (file + clipboard).
- Undo / redo on merge edits.

### Cross-platform
- macOS 10.13+, Windows 10+, Linux (X11 or Wayland; tested on GNOME, KDE, XFCE, Cinnamon, MATE).
- System tray support where the OS permits.
- Light / Dark / System theme; theme persisted across launches.

## Status

This is an active rebuild on top of [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff). The diff engine is intact and reachable from the File menu; the explorer side is new and growing. Roadmap of upcoming work lives in commit messages and release notes.

### Known gaps in v0.1.0
- Directory rows don't roll up the status of their children (a folder containing a modified file shows blank in Status).
- No right-click row actions yet (planned: Reveal in Finder/Explorer, Copy path, Open in `$EDITOR`, `git rm -f`, `git rm --cached`).
- No "Recent folders" list yet.
- No worktree-vs-HEAD diff yet — the plumbing (existing diff engine + go-git) is in place; wiring is on the roadmap.
- No auto-refresh — click Refresh (Cmd/Ctrl+R) after external changes.

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
