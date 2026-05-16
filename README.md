# KrankyBear GitExplorer

A cross-platform GUI git repository explorer built with Go and the Fyne GUI toolkit.

The goal isn't to replace the git CLI or your IDE's git integration — those still own the day-to-day commit / push / branch / rebase workflow. KrankyBearGitExplorer is for the cases where it's easier to *look* than to type: drop a project folder in, see what's tracked, what's modified, what's untracked, on which branch, with what counts. Click `.git` to see the tracked-files tree. Right-click a row for selective edits — reveal, copy path, open in your editor, diff against HEAD, or remove from git.

The two-pane side-by-side diff tool [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff) this app grew out of is preserved as a secondary window (File → Compare Two Files…) and is also wired into the new "Diff against HEAD" workflow. Note that [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff) is still also available as a totally separate application.

## Features

### Folder view

- Open a folder by file picker, drag-and-drop, or keyboard (Cmd/Ctrl+O).
- Recent folders — File → Open Recent Folder lists up to the 10 most recently-opened paths (file picker + drag-and-drop both feed it). Stale paths drop out automatically. The **Recent ▾** toolbar button next to Open Folder pops up the same list inline, one click away.
- Per-entry **Name · Size · Modified · Status** columns. Directories first, alphabetical.
- Filter bar above the list — see [Filter bar](#filter-bar) below.
- The `.git` directory is hidden from the folder list by default — use the **Tracked files** toolbar button (or View → Toggle Tracked Files / Folder View) to inspect what git is tracking. Preferences → "Show .git in folder listings" puts it back in the list for users who want to drill into git's internals.
- Submodule indicator — directories declared in `.gitmodules` show **submodule** in the Status column; ancestor wrapper folders (e.g. `vendor/`) show **contains submodule** so they read distinctly from unrelated plain directories. Clicking a submodule descends into it as its own repo and adds the path to your recent folders.
- Repo header shows current path, branch name, and clean / dirty+untracked file counts when inside a repo, plus the last commit's subject, author, and relative time (italicised) — "(no commits)" for a freshly-init'd repo.

### Filter bar

A single compact row above the list — applies to both the folder view and the tracked-files tree.

- **Filter by name…** — case-insensitive substring match on the file's short name. In the tracked-files tree, a directory whose name matches pulls in all its descendants so you can drill in.
- **Only dirty** — restrict files to those with changes vs HEAD (modified / staged / deleted / conflict).
- **Only untracked** — restrict files to those not yet tracked by git.
- **Only ignored** — restrict files to those excluded by `.gitignore`. Useful for auditing what's being filtered out; implicitly bypasses the "Show ignored" gate so a single tick collapses the view to just the ignored set.
- **Show ignored** — include ignored entries alongside everything else. Off by default; ignored files and ignored directories (e.g. `node_modules/`, `bin/`, `dist/`) are hidden until you opt in.

The three "Only" toggles combine as an OR-union (e.g. ticking dirty + ignored shows both classes side-by-side). Directories and the `.git` pinned row always pass the status gate so navigation stays unbroken; the name filter still applies to them. Filters reset on folder change so each project starts from a clean slate.

Behind the scenes, the folder view consults the repo's `.gitignore` chain via go-git's gitignore matcher — go-git's `Status()` omits ignored files by default (matching the `git status` CLI), so without this "Only ignored" would match zero files.

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

### Repo History

View → Repo History… (or the **History** toolbar button) opens a secondary window:

- **Left pane** — commit list, 200 at a time. Each row shows the commit subject, short SHA, author, and relative time. "Load more commits" appends the next 200.
- **Search across history** — two filter fields above the commit list: "Message contains…" and "Author contains…", both case-insensitive substring. First time you type a query, the iterator drains the full repo log so matching is across all commits, not just the loaded page. The header switches from "N commits loaded" to "N of M commits match" so you can see how aggressive the filter is. Typing is debounced ~150ms; clear both fields to return to the unfiltered list.
- **Right pane** — per-commit detail: full author / email / timestamp, wrapped commit message, and the list of files changed vs the commit's parent with A/M/D action codes.
- Click a changed file → opens the existing two-pane diff in **Historical Diff** mode: both sides read-only, parent blob on the left, this commit's blob on the right. Pane titles include short SHA + ISO date and the commit subject, so parallel comparison windows stay self-identifying.
- Binary blobs (NUL byte in the first 8 KB) get a friendly dialog instead of garbage output.

### Repo Health

View → Repo Health… (also on the tray and the **Health** toolbar button) shows a read-only health snapshot of the current repo:

- Object-database statistics (loose + packed counts, on-disk sizes, pack count, garbage files) parsed from `git count-objects -v`.
- `git fsck --no-progress` verify summary: counts of dangling, unreachable, missing, and broken objects with a sample of the first 10.
- Copy-paste-ready CLI hints — e.g. "287 loose objects — consider running `git -C <repo> gc`".
- **Copy report to clipboard** for sharing into a bug report or chat.

Pure read-only: never invokes `git gc`, `prune`, or any other write operation; the user runs the suggested commands themselves in their CLI.

### Scan Dependencies

View → Scan Dependencies… (also on the tray and the **Scan** toolbar button) runs the `dep-scan` vulnerability scanner against the current folder:

- Non-modal progress dialog while it works.
- Markdown report rendered in a scrollable rich-text dialog with a Copy-to-clipboard button.
- Script resolution prefers the repo's own `dep-scan.sh` / `dep-scan.ps1` (so teammates get a vendored copy on clone — see [README_DEPSCAN.md](README_DEPSCAN.md)) and falls back to the user-level skill install at `~/.claude/skills/dep-scan/`.
- Works on any folder, not just git repos — `dep-scan` walks for manifests across multiple ecosystems (Go, npm, Python, more via OSV).

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
- Keep only one diff window open at a time (default on) — opening a new diff closes any prior one to avoid window explosions when clicking through history. Diff windows with unsaved edits are kept open so work is never silently lost.
- Show .git in folder listings (default off) — surfaces the `.git` directory in the folder view for users who want to drill into git's internals.
- Remember main window size between launches.
- Clear recent files (left / right / both).

### Window management

- **Hide All Windows** snapshots which windows are currently visible and hides exactly those.
- **Show All Windows** restores exactly that snapshot — dialogs you'd already dismissed stay dismissed.

### Cross-repo switch prompt

When you're about to switch the explorer to a different repo (or out of a repo entirely) while repo-bound secondary windows are still open, a modal dialog lists those windows by title and asks what to do:

- **Close them and continue** (default) — fires `Close()` on every registered window so they tear down cleanly via their own intercepts.
- **Keep them open and continue** — proceeds with the switch; you knowingly accept the mixed-repo state.
- **Cancel** — abandons the switch; the explorer stays where it is.

Tracked as repo-bound: **Repo History**, **Diff vs HEAD**, **Historical Diff**. Compare Two Files… is deliberately excluded (it's repo-independent). Navigating within the same repo (clicking subdirs, Up, etc.) doesn't trigger the prompt — only crossing a repo-root boundary does.

### Cross-platform

- macOS 10.13+, Windows 10+, Linux (X11 or Wayland; tested on GNOME, KDE, XFCE, Cinnamon, MATE).
- System tray support where the OS permits.

## Status

This is an active rebuild on top of [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff). The diff engine is intact and reachable from the File menu, the "Diff against HEAD" action, and the Repo History view. Current release: **v0.4.0** — see [ReleaseNotes.txt](ReleaseNotes.txt) for what landed.

### Known gaps as of v0.4.0

- Directory rows don't roll up the status of their children (a folder containing a modified file shows blank in Status; the "Only dirty" filter still navigates fine because directories always pass the status gate).
- No auto-refresh — click Refresh (Cmd/Ctrl+R) or use the toolbar reload after external changes.
- `git rm --cached` shells out to the `git` CLI (`git -C <repo> rm --cached`); requires `git` on `PATH`.
- The Diff against HEAD and Historical Diff views are read-only at the data-mutation level — the per-pane Browse / Recent / Reload buttons on the read-only side are present but no-op; a future polish pass will hide them.

## Dependencies

- [Fyne](https://fyne.io/) v2.7.x — cross-platform Go GUI toolkit (requires OpenGL).
- [go-git/v5](https://github.com/go-git/go-git) — pure-Go git library, used for status / tracked-files / branch info.
- [fyne-tooltip](https://github.com/dweymouth/fyne-tooltip) — tooltip support (vendored).
- Go standard library.

## Building

Create some `compile-*.sh` and `compile-*.ps1` scripts for per-platform builds and `package.sh`for installers if you want.`go build ./...` produces a local binary.

## License

GNU GPL-3.0. See [LICENSE](LICENSE).

## Contributing

Issues and pull requests welcome — small, focused changes please. Design philosophy: align with Fyne's idioms, prefer functionality and correctness over cleverness, keep the dep set tight.

## Author

Allan Marillier

## Acknowledgments

- Built with [Fyne](https://fyne.io/).
- Built on top of [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff), whose two-pane diff engine survives here as the Compare Two Files… view.
