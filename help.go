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
in the visual at-a-glance, plus selective edits (planned: remove from
worktree, remove from index only, diff against HEAD).

The two-pane diff tool that this app grew out of is still available
via File → Compare Two Files… (kept around for general two-file
diffs, and earmarked for a future "current file vs last-committed
version" comparison).

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
• Open Folder… — folder picker (Cmd/Ctrl+O)
• Up arrow — parent folder in folder view; back to folder view from
  tracked view.
• Refresh — re-read the directory listing and re-run git status
  (Cmd/Ctrl+R)
• Tracked files / Back to folder — toggles between the two views

WINDOWS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• View → Show All Windows / Hide All Windows (also on the system tray):
  Hide All snapshots which windows are currently visible and hides
  exactly those; Show All restores that same set. Dialogs you'd
  already dismissed stay dismissed.
• View → Light / Dark / System theme (theme is persisted across launches).
• System tray (where supported): show/hide windows, Open Folder…,
  Compare Two Files…, theme, help, about, check for updates, quit.

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

LIMITATIONS / KNOWN GAPS (and where we're heading)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• Directory rows don't yet roll up the status of children (a folder
  containing a modified file shows blank in the Status column).
• No right-click row actions yet — planned: Reveal in Finder/Explorer,
  Copy path, Open in $EDITOR, "git rm -f", "git rm --cached".
• No "Recent folders" list yet — planned.
• Worktree-vs-HEAD diff (using the Compare Two Files… view to show
  current file on disk vs. the last-committed blob) — planned.
• fsnotify-based auto-refresh on worktree changes — planned.

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
