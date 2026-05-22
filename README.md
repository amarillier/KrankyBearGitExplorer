# KrankyBear GitExplorer

A cross-platform GUI git repository explorer built with Go and the Fyne GUI toolkit.

The goal isn't to replace the git CLI or your IDE's git integration — those still own the day-to-day commit / push / branch / rebase workflow. KrankyBearGitExplorer is for the cases where it's easier to *look* than to type: drop a project folder in, see what's tracked, what's modified, what's untracked, on which branch, with what counts. Click `.git` to see the tracked-files tree. Right-click a row for selective edits — reveal, copy path, open in your editor, diff against HEAD, or remove from git.

The two-pane side-by-side diff tool [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff) this app grew out of is preserved as a secondary window (File → Compare Two Files…) and is also wired into the new "Diff against HEAD" workflow. Note that [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff) is still also available as a totally separate application.

## Requirements

- **`git` on PATH** — most of the explorer runs against go-git directly (folder status, branches, tags, history walk, blame, diff against HEAD), but a handful of operations shell out to the system `git` binary for CLI fidelity and access to bits go-git doesn't expose:
  - `git stash list` (Stashes view)
  - `git reflog` (Reflog viewer)
  - `git log --format=%G?` (signed-commit badges in Repo History)
  - `git rev-list --count` ("What's new since I last looked" banner)
  - `git fsck`, `git count-objects` (Repo Health)
  - `git ls-remote` (remote-sync indicator in the repo header)
  - `git rm --cached` (right-click context menu)

  Without `git` on PATH the explorer's core (folder view, tracked-files view, Blame, Diff against HEAD, Repo History, etc.) still works — the features above degrade silently or display a one-line error in the relevant dialog.
- **Optional**: a `dep-scan` skill or repo-vendored `dep-scan.sh` / `dep-scan.ps1` for the **Scan Dependencies** button — see [README_DEPSCAN.md](README_DEPSCAN.md).

## Features

### Folder view

- Open a folder by file picker, drag-and-drop, or keyboard (Cmd/Ctrl+O).
- Recent folders — File → Open Recent Folder lists up to the 10 most recently-opened paths (file picker + drag-and-drop both feed it). Stale paths drop out automatically. The **Recent ▾** toolbar button next to Open Folder pops up the same list inline, one click away.
- Per-entry **Name · Size · Modified · Status** columns. Directories first, alphabetical. Directory rows roll up the non-clean children below them — `modified (5)` on `src/` means five files in that subtree need attention (severity precedence: conflict > modified > untracked; the count is the total non-clean files in the subtree, not just the headline category).
- Filter bar above the list — see [Filter bar](#filter-bar) below.
- The `.git` directory is hidden from the folder list by default — use the **Tracked files** toolbar button (or View → Toggle Tracked Files / Folder View) to inspect what git is tracking. Preferences → "Show .git in folder listings" puts it back in the list for users who want to drill into git's internals.
- Submodule indicator — directories declared in `.gitmodules` show **submodule** in the Status column; ancestor wrapper folders (e.g. `vendor/`) show **contains submodule** so they read distinctly from unrelated plain directories. Clicking a submodule descends into it as its own repo and adds the path to your recent folders.
- Repo header shows current path, branch name, clean / dirty+untracked file counts when inside a repo, a remote-sync indicator (`in sync with origin/<branch> ✓` / `↑N ↓M vs origin/<branch>` / `no upstream` / `(no remotes)` / `… — remote unreachable` after a probe fails), plus the last commit's subject, author, and relative time (italicised) — "(no commits)" for a freshly-init'd repo.
- **"What's new since I last looked" banner** — a one-row strip just above the filter bar appears when you open a repo whose HEAD has advanced since your last visit: `7 new commits since you last opened this repo (since abc1234).  [Compare] [Dismiss]`. **Compare** opens Repo History with the stored marker pre-set as the compare base — one click to see file-by-file what changed between last visit and now. **Dismiss** hides the banner and advances the marker. Per-repo marker persisted in Fyne preferences (~50 bytes per repo); first visit silently captures the marker so you don't see a banner on a repo's debut. The marker also advances on cross-repo navigation, window close, and Quit, so closing the explorer counts as implicit acknowledgement. Force-push / pruned / behind-marker edge cases re-anchor silently to current HEAD.

### Filter bar

A single compact row above the list — applies to both the folder view and the tracked-files tree.

- **Filter by name…** — case-insensitive substring match on the file's short name. In the tracked-files tree, a directory whose name matches pulls in all its descendants so you can drill in.
- **Only dirty** — restrict to files (and directories, via rollup) with changes vs HEAD (modified / staged / deleted / conflict).
- **Only untracked** — restrict to files / directories whose subtree contains untracked entries.
- **Only ignored** — restrict files to those excluded by `.gitignore`. Useful for auditing what's being filtered out; implicitly bypasses the "Show ignored" gate so a single tick collapses the view to just the ignored set.
- **Show ignored** — include ignored entries alongside everything else. Off by default; ignored files and ignored directories (e.g. `node_modules/`, `bin/`, `dist/`) are hidden until you opt in.

The three "Only" toggles combine as an OR-union (e.g. ticking dirty + ignored shows both classes side-by-side). Directory rollup powers the dirty / untracked filters for folders too — ticking "Only dirty" collapses the view to folders that contain dirty content anywhere inside, not all folders. Submodule rows are treated as opaque (a separate repo) and always pass these gates so you can drill in. The `.git` pinned row bypasses all filters. Filters reset on folder change so each project starts from a clean slate.

Behind the scenes, the folder view consults the repo's `.gitignore` chain via go-git's gitignore matcher — go-git's `Status()` omits ignored files by default (matching the `git status` CLI), so without this "Only ignored" would match zero files.

### Tracked-files view

- Tree of everything git is tracking inside the repo, expandable by directory.
- Non-clean leaves are annotated with a human-readable status (`modified`, `staged`, `untracked`, `ignored`, etc.) — full mapping via View → Git Status Legend… or the tooltip on the Status column header.
- Branch nodes carry the same directory rollup as the folder view — e.g. `src/   [modified (5)]` — so you can spot subtree changes without expanding.
- Up arrow returns to the folder view of the same repo (one logical level back), not the filesystem parent.

### Row actions (right-click)

Right-click any file or directory row in the folder view to act on it. Items enable/disable per row based on the file's git state — tracked, untracked, ignored, directory, or `.git`.

**Inspection** (any file):

- **Reveal in Finder / Explorer / file manager** — cross-platform; uses `open -R` on macOS, `explorer /select` on Windows, `xdg-open` on Linux.
- **Copy path** — absolute path to the clipboard.
- **Open in `$EDITOR`** — honours `$VISUAL` then `$EDITOR` (supports `"code --wait"`-style commands with flags); falls back to the OS default open command.

**Diff & history** (tracked, already-committed files):

- **Blame…** — see [Blame](#blame) below.
- **Diff against HEAD…** — see [Diff against HEAD](#diff-against-head) below.
- **Show history for this file…** — opens Repo History pre-filtered to commits that touched this path. See [Repo History](#repo-history).

**Lifecycle** (untracked / ignored files):

- **git add (start tracking)…** — stages an untracked file via go-git's `wt.Add`. After staging the file flips to *added (staged)* status.
- **Add to .gitignore…** — appends an anchored entry (`/path/to/file`) to the repo's root `.gitignore`, creating it if needed. Anchored on purpose so it matches exactly that path, not every file of that name in the tree.
- **Un-ignore (allow tracking)…** — symmetric undo for ignored files. If `.gitignore` has an exact-match line, that line is removed; otherwise the file is being ignored by a broader pattern (e.g. `*.sh`) and an anchored negation entry (`!/path/to/file`) is appended instead. The confirm dialog tells you which branch will be taken.
- **Delete from disk…** — `os.Remove` with a destructive-action confirm. Offered for untracked or ignored files only (tracked files have `git rm` below); files only, not directories.

**Remove from git** (tracked files):

- **git rm -f** — remove from worktree + index, with a confirm dialog (uses go-git's `wt.Remove`).
- **git rm --cached** — remove from index only, keep the file on disk; pairs nicely with **Add to .gitignore** above.

### Blame

Right-click a tracked file → **Blame…** (or pick **Blame…** from the new **View ▾** toolbar dropdown when a tracked file is selected in the folder list) to open a per-line annotated view of the file at HEAD:

- Each row shows **line number · short SHA · YYYY-MM-DD · author · code** in a monospaced layout so the gutter columns line up. Built on go-git's `Blame()`.
- Compute is async — go-git walks the commit graph to attribute each line, which can take a few seconds on long-lived files. A "Hide (continues in background)" progress dialog appears while it runs; the window opens when blame completes.
- Click any row → opens a Historical Diff for that file at the blame commit vs its parent, both sides read-only. Closes the loop with the existing history-detail flow: blame becomes the natural entry point into "what changed on this line and why".
- Same enable rule as **Diff against HEAD**: the file must be tracked and already committed at least once. Untracked / ignored / freshly-added-staged rows have no Blame… menu entry, and the View ▾ dropdown's Blame… item is disabled when no eligible file is selected.
- Long source lines truncate with an ellipsis in the row — click through to the historical diff for the full content.

### Diff against HEAD

Right-click a tracked file → **Diff against HEAD…** to open the existing two-pane diff with:

- **Left** = the HEAD blob, labelled `HEAD@<short-sha>: <relative-path>  (read-only)`, marked read-only.
- **Right** = the current worktree file, fully editable.

Edits flow only from HEAD → worktree (via the existing "Apply left to right" merge action). The reverse direction is blocked so you can't accidentally mutate the in-memory HEAD buffer. Read-only enforcement is symmetric — Swap left/right keeps the HEAD content protected on whichever side it now occupies.

### Repo History

View → Repo History… (or the **History** toolbar button) opens a secondary window:

- **Left pane** — commit list, 200 at a time. Each row shows the commit subject, short SHA, author, and relative time. "Load more commits" appends the next 200.
- **Search across history** — two filter fields above the commit list: "Message contains…" and "Author contains…", both case-insensitive substring. First time you type a query, the iterator drains the full repo log so matching is across all commits, not just the loaded page. The header switches from "N commits loaded" to "N of M commits match" so you can see how aggressive the filter is. Typing is debounced ~150ms; clear both fields to return to the unfiltered list.
- **File-level history** — right-click any tracked file in the explorer → **Show history for this file…** opens this window pre-filtered (via go-git's `LogOptions.FileName`) to just commits that touched that file. Window title reads `File History: <repo> — <path>`, with an italic indicator row + toggle button at the top: **Show all history** broadens back to the full log, leaves a breadcrumb (`Last file: <path>`), and flips the button to **Back to file history** so one more click returns you to the file-scoped view.
- **Right pane** — per-commit detail: full author / email / timestamp, wrapped commit message, and the list of files changed vs the commit's parent with A/M/D action codes. Click a changed file → opens the existing two-pane diff in **Historical Diff** mode: both sides read-only, parent blob on the left, this commit's blob on the right.
- **Compare any two commits** — click a commit, then the **Pick as compare base** button in the detail-pane header (checkmark icon). A banner appears at the top of the commit list (`Compare base: <SHA> — <subject>` + Cancel compare). Click any other commit → right pane switches to `Compare: <baseSHA> → <selectedSHA>` mode, showing files changed between the two commits. Click a file → opens a Historical Diff between the two arbitrary commits. While in compare mode the diff window auto-follows your selection — scrub through commits and the diff updates for the same file (driven by the existing "Keep only one diff window" preference; closed diffs stay closed).
- Pane titles in the diff include short SHA + ISO date and the commit subject, so parallel comparison windows stay self-identifying.
- Binary blobs (NUL byte in the first 8 KB) get a friendly dialog instead of garbage output.
- **Signed-commit badges** — each commit row carries a small left-gutter glyph for its signature status: **✓** valid · **✗** bad / expired / revoked · **?** signed but unverifiable on this machine (signer's public key isn't in your local keyring) · blank for unsigned. The detail pane mirrors with an italic `Signature: …` line. Built on `git log --format=%G?` so it uses your local GPG/SSH configuration; loaded asynchronously so the window opens immediately and badges populate when the log walk finishes. Graceful degrade if `git` isn't on PATH — the rest of the history view still works without badges.

### Reflog viewer

View → Reflog… (also in the View ▾ toolbar dropdown and the tray's data-views section) — a read-only "here's where HEAD has been" panel for the current repo:

- **Left pane** — list of reflog entries: `HEAD@{N}` · date · action (commit / checkout / reset / rebase / pull / ...) · message. Columns are fixed-width with the message flexing to the remaining space.
- **Right pane** — when an entry is selected, shows the entry's commit detail: short SHA, author, ISO timestamp, the reflog action line, the commit's message, and the list of files changed vs the commit's parent.
- Click a file in the right pane → opens a Historical Diff for that file at the entry's commit vs its parent, both sides read-only. Same flow as Repo History's detail pane.
- **Unreachable commits** still resolve — entries pointing at commits that fell out of the regular log (after a hard reset or rebase) are still in the object DB because the reflog itself keeps them alive. Useful for "I rebased and lost something" recovery scenarios. If the commit truly isn't there (pruned), the detail pane shows a graceful fallback message instead of erroring.
- Shells out to `git -C <repo> reflog --date=iso --format=…` with NULL-byte field separators so subject text containing `:` or `|` doesn't trip the parser.

### Repo Health

View → Repo Health… (also on the tray and the **Health** toolbar button) shows a read-only health snapshot of the current repo:

- **Repository overview** — HEAD branch + short SHA, last-commit timestamp with relative-age hint (`2h ago`), commit subject + author, counts of local branches / tags / remotes. Read via go-git.
- Object-database statistics (loose + packed counts, on-disk sizes, pack count, garbage files) parsed from `git count-objects -v`.
- `git fsck --no-progress` verify summary: counts of dangling, unreachable, missing, and broken objects with a sample of the first 10.
- Copy-paste-ready CLI hints — e.g. "287 loose objects — consider running `git -C <repo> gc`".
- **Copy report to clipboard** for sharing into a bug report or chat (includes the overview section too).

Pure read-only: never invokes `git gc`, `prune`, or any other write operation; the user runs the suggested commands themselves in their CLI.

### Repo views: Branches / Tags / Remotes / Contributors / Stashes

Five small read-only listings under the View menu, the system tray, and the **View ▾** toolbar dropdown (a one-click curated menu of all the data views: Blame…, Branches…, Contributors…, Git Status Legend…, Reflog…, Remotes…, Repo Health…, Repo History…, Stashes…, Tags…, alphabetised; disabled when no repo is loaded). Pure display; no checkout / create / delete buttons — that line stays with the git CLI.

- **Branches…** / **Tags…** — name · short SHA · commit date · subject, sorted newest-first. Annotated tags resolved through their tag object to the underlying commit.
- **Remotes…** — name · URL. A remote with separate fetch and push URLs gets one row per URL.
- **Contributors…** — per-author roll-up: name + email, commit count, first-contribution date, last-contribution date, and lines added / removed via go-git's per-commit `Stats()`. Sorted by commit count descending; footer line totals everything across authors. Computed synchronously when the dialog opens — fast for typical repos, may take a moment on very large histories.
- **Stashes…** — enumerates stashes via `git stash list`. Each row is `stash@{N}` · subject · date. Click a row → per-stash detail dialog with files touched; click a file → Historical-Diff-style read-only diff between the stash and its base commit.

All five dialogs use a Border layout where the wide column (Subject / URL / Author) takes the remaining space and narrow columns stay tight; resize the dialog wider and the wide column grows.

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
- Reachable from the explorer's File menu, the system tray, and the **Diff** toolbar button (appended after Scan — always enabled, no repo required).

### Preferences

File → Preferences… (from either window) opens a single dialog:

- Theme (Light / Dark / System) — applies app-wide, persisted.
- Diff-view defaults: line numbers, visible whitespace, sync scroll — take effect on the next-opened diff window.
- Keep only one diff window open at a time (default on) — opening a new diff closes any prior one to avoid window explosions when clicking through history. Diff windows with unsaved edits are kept open so work is never silently lost.
- Show .git in folder listings (default off) — surfaces the `.git` directory in the folder view for users who want to drill into git's internals.
- Auto-refresh when files change outside the app (default on) — watches the current folder + `.git/index` via fsnotify; external edits (your IDE saving a file, a CLI `git commit`, a branch switch) update the explorer automatically. Events are debounced ~250ms so a single `$EDITOR` save coalesces into one refresh. Watching is non-recursive — just the visible folder + the repo's index file — so handle counts stay bounded on large repos.
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

This is an active rebuild on top of [KrankyBearDiff](https://github.com/amarillier/KrankyBearDiff). The diff engine is intact and reachable from the File menu, the **Diff** toolbar button, the "Diff against HEAD" action, the Blame viewer click-through, the Repo History view, and the Reflog viewer. Current release: **v0.7.0** — see [ReleaseNotes.txt](ReleaseNotes.txt) for what landed.

### Known gaps as of v0.7.0

- The remote-sync indicator's ahead/behind counts come from cached refs — they only refresh when *you* fetch (CLI or git client). The async live probe checks reachability but doesn't actually fetch. Run `git fetch` from your terminal to update the cached counts.
- Directory rollup excludes ignored files — go-git's `Status()` doesn't enumerate them and walking the gitignore matcher recursively for every directory listing would slow down large repos. Use the file-level **Only ignored** filter for `.gitignore` auditing.
- fsnotify auto-refresh is non-recursive — changes deep inside a subdirectory still need a manual Cmd/Ctrl+R.
- `git rm --cached` shells out to the `git` CLI (`git -C <repo> rm --cached`); requires `git` on `PATH`. Same for `git stash list`, `git count-objects`, `git fsck`, `git ls-remote`, and the local sync read.
- The Diff against HEAD and Historical Diff views are read-only at the data-mutation level — the per-pane Browse / Recent / Reload buttons on the read-only side are present but no-op; a future polish pass will hide them.

## Screenshots
Coming, not here yet

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
