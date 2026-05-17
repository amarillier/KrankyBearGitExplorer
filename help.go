package main

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var helpWindow fyne.Window

// showHelp displays help for KrankyBear GitExplorer.
func showHelp(a fyne.App) {
	if helpWindow != nil && helpWindow.Content().Visible() {
		windowShow(helpWindow)
		helpWindow.RequestFocus()
		return
	}

	helpWindow = a.NewWindow(appName + " - Help")
	helpWindow.SetIcon(resourceKrankyBearNerdPng)

	icon := newBrandingDialogImage(resourceKrankyBearNerdPng)

	helpText := `KrankyBear GitExplorer — visual git repository explorer

OVERVIEW
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Open a project folder and see at a glance: its contents (name, size,
modified date), whether each file is tracked / modified / untracked /
ignored / staged, and the current git branch with a clean vs. dirty
file count. Click the .git row to switch into a tree of everything
git is tracking inside the repo.

Day-to-day git work — committing, pushing, branching, rebasing — is
expected to happen in your IDE or the git CLI. GitExplorer's value is
in the visual at-a-glance plus selective edits: right-click any file
row for Reveal, Copy path, Open in $EDITOR, Blame…, Diff against
HEAD…, Show history for this file…, git add, Add to / Un-ignore in
.gitignore, git rm -f, git rm --cached, Delete from disk.

The two-pane diff tool that this app grew out of is still available
via File → Compare Two Files… and the new Diff toolbar button (and
is wired into Diff against HEAD, Blame click-through, and the Repo
History detail pane).

OPENING A FOLDER
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• File → Open Folder… (Cmd/Ctrl+O), or the Open Folder… button in the toolbar.
• Drag and drop a folder onto the window. Drop a file instead and the
  containing folder is opened.
• Click a directory row to descend into it; click the Up arrow to
  return to the parent.

REPO HEADER
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
When the opened folder is inside a git repository the header shows:
• the absolute path of the current folder
• the current branch name (or "(no commits)" / "(not a git repository)")
• a clean vs. dirty/untracked file count for the whole worktree

FOLDER VIEW (default)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
The main pane lists the contents of the current folder:
• Name — directories shown with a trailing "/", listed first.
• Size — byte count for files; "<DIR>" for directories.
• Modified — file modification time (YYYY-MM-DD HH:MM).
• Status — git status for the file, if the folder is inside a repo.
  Hover the "Status" column header for a quick legend; full reference
  is at View → Git Status Legend…

The .git directory is always shown first. Clicking it switches to
the tracked-files view (see below) — the same as clicking the
"Tracked files" toolbar button.

TRACKED-FILES VIEW
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
A tree of every path git is tracking inside this repo, expandable by
directory. Non-clean leaves show their human-readable status in
square brackets (e.g. "main.go   [modified]").

Switch in by clicking .git in the folder view OR the "Tracked files"
toolbar button. Switch back by clicking "Back to folder", the Up
arrow (which behaves as "back" from tracked view), or
View → Toggle Tracked Files / Folder View.

TOOLBAR
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• Open Folder… — folder picker (Cmd/Ctrl+O).
• Recent ▾ — popup list of recently-opened folders (matches
  File → Open Recent Folder).
• Up arrow — parent folder in folder view; back to folder view from
  tracked view.
• Refresh — re-read the directory listing and re-run git status
  (Cmd/Ctrl+R).
• Tracked files / Back to folder — toggles between the two views.
• View ▾ — popup of data views (Blame…, Branches…, Contributors…,
  Git Status Legend…, Remotes…, Repo Health…, Repo History…,
  Stashes…, Tags…) for one-click access. Disabled when no repo is
  loaded; Blame… is only enabled when a tracked, already-committed
  file is selected in the folder list.
• History — opens Repo History (same as View → Repo History…).
• Health — opens Repo Health (same as View → Repo Health…).
• Scan — runs Scan Dependencies (same as File → Scan Dependencies…).
• Diff — opens the two-pane diff window for ad-hoc compare of any
  two files from your filesystem (same as File → Compare Two
  Files…). Always enabled — no repo required.

BLAME
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Right-click a tracked file → "Blame…" (or open it from the View ▾
toolbar dropdown when a tracked file is selected) to open a per-line
annotated view of the file at HEAD: line number · short SHA ·
YYYY-MM-DD · author · code, monospaced so the gutter columns align.

Built on go-git's Blame(); compute is async with a progress dialog
since walking the commit graph for attribution can take a few
seconds on long-lived files.

Click any row → opens a Historical Diff for that file at the blame
commit vs its parent, both sides read-only. The blame becomes a
natural entry point into "what changed on this line, and why".

Same enable rule as Diff against HEAD: the file must be tracked and
already committed at least once. Long source lines truncate with an
ellipsis in the row — click through to the historical diff for the
full content.

WINDOWS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• View → Show All Windows / Hide All Windows (also on the system tray):
  Hide All snapshots which windows are currently visible and hides
  exactly those; Show All restores that same set. Dialogs you'd
  already dismissed stay dismissed.
• View → Light / Dark / System theme (theme is persisted across launches).
• System tray (where supported), organised into sections:
    - Hide / Show All Windows
    - Non-view actions: Compare Two Files…, Open Folder…, Open
      Recent Folder, Preferences…, Scan Dependencies…
    - Data views: Branches…, Contributors…, Git Status Legend…,
      Remotes…, Repo Health…, Repo History…, Stashes…, Tags…
    - Theme: Dark / Light / System
    - About, Check for Updates…, Help
    - Quit

COMPARE TWO FILES… (legacy diff tool)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
File → Compare Two Files… opens the two-pane side-by-side diff in a
secondary window. Drag files onto its panes (or use Browse…), and
use its own menu bar for save / merge / export-patch operations.
Closing this window leaves GitExplorer running; reopen it from the
File menu at any time.

UPDATES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Help → Check for Updates… queries GitHub for the latest release
of KrankyBearGitExplorer and compares against your local version.
On startup the app may also check in the background about once every
seven days; if a newer release exists, an update dialog appears.
Failed background checks are silent.

LIMITATIONS / KNOWN GAPS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• Remote-sync indicator's ahead/behind counts come from cached refs —
  they only refresh when you fetch (CLI or git client). Run
  'git fetch' from your terminal to update the cached counts.
• Directory rollup excludes ignored files — use the file-level
  "Only ignored" filter for .gitignore auditing.
• fsnotify auto-refresh is non-recursive — changes deep inside a
  subdirectory still need a manual Cmd/Ctrl+R.
• Several less-common operations shell out to the 'git' CLI
  (git rm --cached, git stash list, git count-objects, git fsck,
  git ls-remote) — they require 'git' on PATH.
• Blame on a long-lived file can take several seconds while go-git
  walks the commit graph — the progress dialog has a "Hide
  (continues in background)" option for that case.

KEYBOARD
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• Cmd/Ctrl O — Open Folder…
• Cmd/Ctrl R — Refresh current folder + git status
• Cmd/Ctrl Q — Quit (also File → Quit)

The Compare Two Files… window has its own set of shortcuts
(Cmd/Ctrl Z/Y for undo/redo, Shift+Cmd/Ctrl E for export patch,
Alt , / Alt . for prev/next change, etc.) — see that window's
menu items for the full list.

MORE INFORMATION
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• GitHub: https://github.com/amarillier/KrankyBearGitExplorer
• License: https://github.com/amarillier/KrankyBearGitExplorer/blob/allanm/LICENSE
`

	helpLabel := widget.NewLabel(helpText)
	helpLabel.Wrapping = fyne.TextWrapWord

	githubURL, _ := url.Parse("https://github.com/amarillier/KrankyBearGitExplorer")
	githubLink := widget.NewHyperlink("Visit GitHub Repository", githubURL)
	githubLink.Alignment = fyne.TextAlignCenter

	licenseURL, _ := url.Parse("https://github.com/amarillier/KrankyBearGitExplorer/blob/allanm/LICENSE")
	licenseLink := widget.NewHyperlink("View License", licenseURL)
	licenseLink.Alignment = fyne.TextAlignCenter

	scrollContent := container.NewScroll(helpLabel)
	scrollContent.SetMinSize(fyne.NewSize(560, 480))

	header := widget.NewLabelWithStyle(appName+" — Help", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	footer := container.NewVBox(
		widget.NewSeparator(),
		container.NewCenter(container.NewHBox(githubLink, licenseLink)),
	)

	readingArea := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		nil,
		nil,
		nil,
		scrollContent,
	)

	mainArea := container.NewHBox(
		container.NewPadded(icon),
		readingArea,
	)

	content := container.NewBorder(
		nil,
		footer,
		nil,
		nil,
		mainArea,
	)

	helpWindow.SetContent(container.NewPadded(content))
	helpWindow.Resize(fyne.NewSize(900, 650))

	helpWindow.SetCloseIntercept(func() {
		windowHide(helpWindow)
	})

	windowShow(helpWindow)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
