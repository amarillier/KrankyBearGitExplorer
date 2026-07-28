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
.gitignore, git rm -f, git rm --cached, Untrack & ignore (both in one
step), Delete from disk.

The two-pane diff tool that this app grew out of is still available
via File → Compare Two Files… and the new Diff toolbar button (and
is wired into Diff against HEAD, Blame click-through, and the Repo
History detail pane).

REQUIREMENTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Most of the explorer runs against an embedded go-git library
directly (folder status, branches, tags, history walk, blame, diff
against HEAD), but a handful of operations shell out to the system
'git' binary for CLI fidelity and access to bits go-git doesn't
expose. These features need 'git' on PATH:

• 'git stash list'        — Stashes view
• 'git reflog'            — Reflog viewer
• 'git log --format=%G?'  — signed-commit badges in Repo History
• 'git rev-list --count'  — "What's new since I last looked" banner
• 'git fsck'              — Repo Health verify summary
• 'git count-objects -v'  — Repo Health object DB statistics
• 'git ls-remote'         — remote-sync indicator in the repo header
• 'git rm --cached'       — right-click context menu

Without 'git' installed the explorer's core (folder view,
tracked-files view, Blame, Diff against HEAD, Repo History, etc.)
still works — the features above degrade silently or show a
one-line error in the relevant dialog.

Optional: a 'dep-scan' skill or a repo-vendored 'dep-scan.sh' /
'dep-scan.ps1' for the Scan Dependencies button (see
README_DEPSCAN.md in the repo).

DEPENDENCY SCANNING
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• File → Scan Dependencies… (or the Scan toolbar button) scans the
  current folder for vulnerable dependencies.
• File → Scan All Repos… sweeps every git repo discovered under your
  configured source folders in one pass, with one aggregated report.
  Use Configure Repos to Scan… to set the source folders and to skip
  individual repos. The header shows a dependency-health badge from
  the last sweep; View Last Scan Report… reopens it.

• File → Audit Local Repos… sweeps the same configured source folders
  and reports, read-only, which repos need attention: local-only (no
  remote), unpushed commits (ahead of the remote), uncommitted work,
  or initialized-but-empty. Clean, in-sync repos are omitted. Handy for
  spotting work you've committed locally but never pushed anywhere.

• The toolbar "Scan ▾" button gathers every dependency-scan action
  in one menu: local dep-scan or GitHub Dependabot, for this repo or
  all repos, plus their configuration. The same actions are mirrored
  in the Repo ▾ dropdown.

• Dependabot: Check This Repo… queries GitHub's Dependabot alerts for
  the current repo (open alerts, with Open advisory and, for Go
  modules with a published fix, Apply fix). Dependabot: Scan All
  Repos… checks every repo under the owners you configure (Dependabot:
  Configure Owners…) plus your local clones, and shows a per-repo
  summary. Repos that can't be scanned are grouped by reason — third-
  party clones you don't own, Dependabot-not-enabled (with how-to-fix
  advice), 404, or auth — instead of a vague error. A 🛡 header badge
  reflects the last sweep.
  Hosts: github.com, plus GitHub Enterprise Server (e.g. internal
  hosts) for repos you've cloned locally — run
  'gh auth login --hostname <host>' to enable that host. GHES org-wide
  enumeration is planned.
  Requires the gh CLI installed and authenticated (gh auth login).
  Tip: if an Enterprise scan fails after your VPN/session timed out,
  you usually don't need a full re-login — just refresh the token with
  'gh auth refresh -h <host>'.
  Local dep-scan = offline/uncommitted code; Dependabot = GitHub's
  authoritative view of your pushed state.

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

"WHAT'S NEW SINCE I LAST LOOKED" BANNER
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
A one-row strip just above the filter bar appears when you open a
repo whose HEAD has advanced since your last visit:

  "7 new commits since you last opened this repo (since abc1234)."
                                            [Compare] [Dismiss]

• Compare → opens Repo History with the stored marker pre-set as
  the compare base. One click to see file-by-file what changed
  between last visit and now.
• Dismiss → hides the banner and marks current HEAD as "seen".

The per-repo marker is persisted via Fyne preferences (~50 bytes
per repo). First visit to a repo silently captures the marker — no
banner on a repo's debut, only when something changed between
visits. The marker also advances on cross-repo navigation, window
close, and Quit, so closing the explorer counts as implicit
acknowledgement.

Force-push, pruned commits, or HEAD-behind-marker edge cases
silently re-anchor to current HEAD and skip the banner.

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
  Git Status Legend…, Reflog…, Remotes…, Repo Health…, Repo
  History…, Stashes…, Tags…) for one-click access. Disabled when
  no repo is loaded; Blame… is only enabled when a tracked,
  already-committed file is selected in the folder list.
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

REFLOG VIEWER
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
View → Reflog… (also in the View ▾ toolbar dropdown and the tray's
data-views section) opens a read-only "here's where HEAD has been"
panel for the current repo. Master/detail layout:

• Left pane — list of reflog entries: HEAD@{N} · date · action
  (commit / checkout / reset / rebase / pull / ...) · message.
• Right pane — selected entry's commit detail + file list (changes
  vs the commit's parent).
• Click a file in the right pane → opens a Historical Diff (file
  at that commit vs its parent, both sides read-only). Same flow
  as Repo History's detail pane.

Unreachable commits still resolve — entries pointing at commits
that fell out of the regular log (after a hard reset or rebase)
remain in the object DB because the reflog itself keeps them alive.
Useful for "I rebased and lost something" recovery scenarios. If
the commit truly isn't there (pruned), the detail pane shows a
graceful fallback message.

SIGNED-COMMIT BADGES IN REPO HISTORY
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
The Repo History window's left-pane commit list now shows a small
gutter glyph for each commit's signature status:
• ✓  valid signature
• ✗  bad / expired / revoked
• ?  signed but unverifiable on this machine (the signer's public
     key isn't in your local keyring)
• (blank) — unsigned

The detail pane gets a matching italic "Signature: …" line between
the header and the commit message.

Built on 'git log --format=%G?' so it uses your local GPG/SSH
configuration. To turn a "?" into a "✓" for a commit you trust,
import the signer's public key — e.g. 'gpg --recv-keys <keyid>' or
your existing keyserver workflow.

Loaded asynchronously in the background — the window opens
immediately and badges populate when the git log walk completes
(typically <1s, a few seconds on huge repos). Graceful degrade if
'git' isn't on PATH (window still works, no badges).

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
      Reflog…, Remotes…, Repo Health…, Repo History…, Stashes…,
      Tags…
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
• Several operations shell out to the 'git' CLI — see the
  REQUIREMENTS section above for the full list.
• Blame on a long-lived file can take several seconds while go-git
  walks the commit graph — the progress dialog has a "Hide
  (continues in background)" option for that case.
• "What's new since I last looked" banner re-evaluates only on
  repo open / cross-repo switch, not on fsnotify auto-refresh
  inside the same repo. A CLI commit made while the explorer is
  open updates the file list immediately (via fsnotify) but won't
  pop the banner mid-session — the marker advances on close, so
  it's correct end-to-end.
• Signed-commit badges in Repo History reflect what 'git log
  --format=%G?' reports against your local GPG/SSH keyring — a
  '?' means the signature is there but the signer's public key
  isn't in your local keyring, NOT that the signature is invalid.

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
