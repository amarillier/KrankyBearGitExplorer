package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// git_management.go holds the small write-ops surface introduced in v0.8.0:
// initialise a repo and set the local user.name/user.email identity. This
// is the first time the explorer crosses the read-only line for repo-level
// state (file-level writes — add, rm, .gitignore edits — landed earlier).
// Heavier ops (commit, pull, push, remote management) will join this file
// in subsequent v0.8.x releases.

// globalInitDefaultBranch reads the user's global init.defaultBranch
// preference. Returns "master" if the setting is absent — matches
// go-git's hard default and Allan's stated "if not set, old-style"
// preference. Any git CLI error (no git on PATH, etc.) collapses to the
// same "master" fallback so the dialog still has a sensible seed.
func globalInitDefaultBranch() string {
	out, err := exec.Command("git", "config", "--global", "--get", "init.defaultBranch").Output()
	if err != nil {
		return "master"
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "master"
	}
	return name
}

// validateBranchName applies a minimal subset of git's ref-name rules —
// enough to catch obvious typos and characters git will refuse, without
// trying to implement git-check-ref-format exhaustively. If a name slips
// through this but git refuses it, the underlying PlainInit error
// surfaces verbatim in the failure dialog.
func validateBranchName(name string) error {
	if name == "" {
		return fmt.Errorf("branch name is required")
	}
	if strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("branch name cannot contain whitespace")
	}
	if strings.ContainsAny(name, "~^:?*[\\") {
		return fmt.Errorf("branch name cannot contain any of: ~ ^ : ? * [ \\")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("branch name cannot start with '-'")
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("branch name cannot end with '.' or '.lock'")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("branch name cannot contain '..'")
	}
	return nil
}

// showInitRepoDialog walks the user through creating a new git repository
// at the explorer's current folder. The initial branch field is seeded
// from the user's global init.defaultBranch so the common case is a
// single click; users who prefer a project-specific branch name (e.g.
// to match a chosen remote shorthand) can overwrite it here rather than
// running `git branch -M <name>` afterward. On success the supplied
// onDone callback runs — typically a view refresh so the header
// repaints with the freshly-detected repo.
func showInitRepoDialog(parent fyne.Window, currentPath string, onDone func()) {
	if currentPath == "" {
		dialog.ShowInformation("No folder",
			"Open a folder first — initialize creates a .git directory in the currently-open folder.",
			parent)
		return
	}

	pathLabel := widget.NewLabel(currentPath)
	pathLabel.Wrapping = fyne.TextWrapWord
	pathLabel.TextStyle = fyne.TextStyle{Monospace: true}

	branchEntry := widget.NewEntry()
	branchEntry.SetText(globalInitDefaultBranch())

	note := widget.NewLabel("Existing files in this folder are not modified. A .git directory will be created alongside them.")
	note.Wrapping = fyne.TextWrapWord
	note.TextStyle = fyne.TextStyle{Italic: true}

	form := widget.NewForm(
		widget.NewFormItem("Folder", pathLabel),
		widget.NewFormItem("Initial branch", branchEntry),
	)
	content := container.NewVBox(form, note)

	d := dialog.NewCustomConfirm("Initialize Repository", "Initialize", "Cancel",
		content, func(ok bool) {
			if !ok {
				return
			}
			name := strings.TrimSpace(branchEntry.Text)
			if err := validateBranchName(name); err != nil {
				dialog.ShowError(err, parent)
				return
			}
			if err := initRepoAt(currentPath, name); err != nil {
				dialog.ShowError(fmt.Errorf("initialize repository: %w", err), parent)
				return
			}
			dialog.ShowInformation("Repository initialized",
				fmt.Sprintf("Created .git in %s on branch %q.\n\nNothing is committed yet — HEAD is unborn. The first commit will create the branch ref.", currentPath, name),
				parent)
			if onDone != nil {
				onDone()
			}
		}, parent)
	d.Resize(fyne.NewSize(560, 280))
	d.Show()
}

// initRepoAt creates a non-bare repo at path with the given initial
// branch name. The branch ref itself doesn't exist yet (no commits) —
// HEAD is a symbolic ref pointing at refs/heads/<branch> and the first
// commit will create the ref.
func initRepoAt(path, branch string) error {
	opts := &git.PlainInitOptions{
		InitOptions: git.InitOptions{
			DefaultBranch: plumbing.ReferenceName("refs/heads/" + branch),
		},
		Bare: false,
	}
	_, err := git.PlainInitWithOptions(path, opts)
	return err
}

// showRepoIdentityDialog opens the local repo identity editor (user.name
// + user.email). Fields are seeded from the repo's current .git/config
// values; blank values are treated as "no change" on Save to avoid
// accidentally clearing a configured identity. Writes go via `git
// config --local`, mirroring the read pattern used by the v0.7.1 Repo
// Config view.
func showRepoIdentityDialog(parent fyne.Window, repoRoot string, onSaved func()) {
	if repoRoot == "" {
		dialog.ShowInformation("No repository",
			"Open a folder that is inside a git repository first, then try again.",
			parent)
		return
	}

	currentName := readLocalConfigValue(repoRoot, "user.name")
	currentEmail := readLocalConfigValue(repoRoot, "user.email")

	nameEntry := widget.NewEntry()
	nameEntry.SetText(currentName)
	nameEntry.SetPlaceHolder("e.g. Allan Marillier")

	emailEntry := widget.NewEntry()
	emailEntry.SetText(currentEmail)
	emailEntry.SetPlaceHolder("e.g. you@example.com")

	intro := widget.NewLabel("These values are saved to .git/config and override your global ~/.gitconfig identity for this repo only.")
	intro.Wrapping = fyne.TextWrapWord
	intro.TextStyle = fyne.TextStyle{Italic: true}

	hint := widget.NewLabel("Blank fields are treated as \"no change\" — clear an identity from the CLI with `git config --local --unset user.name`.")
	hint.Wrapping = fyne.TextWrapWord
	hint.TextStyle = fyne.TextStyle{Italic: true}

	form := widget.NewForm(
		widget.NewFormItem("user.name", nameEntry),
		widget.NewFormItem("user.email", emailEntry),
	)
	content := container.NewVBox(intro, form, hint)

	d := dialog.NewCustomConfirm("Local Repo Identity", "Save", "Cancel",
		content, func(ok bool) {
			if !ok {
				return
			}
			newName := strings.TrimSpace(nameEntry.Text)
			newEmail := strings.TrimSpace(emailEntry.Text)
			var changed []string
			if newName != "" && newName != currentName {
				if err := writeLocalConfigValue(repoRoot, "user.name", newName); err != nil {
					dialog.ShowError(fmt.Errorf("set user.name: %w", err), parent)
					return
				}
				changed = append(changed, "user.name")
			}
			if newEmail != "" && newEmail != currentEmail {
				if err := writeLocalConfigValue(repoRoot, "user.email", newEmail); err != nil {
					dialog.ShowError(fmt.Errorf("set user.email: %w", err), parent)
					return
				}
				changed = append(changed, "user.email")
			}
			if len(changed) == 0 {
				dialog.ShowInformation("No changes",
					"Nothing to save — both fields were blank or unchanged.",
					parent)
				return
			}
			dialog.ShowInformation("Identity saved",
				"Updated: "+strings.Join(changed, ", "),
				parent)
			if onSaved != nil {
				onSaved()
			}
		}, parent)
	d.Resize(fyne.NewSize(540, 320))
	d.Show()
}

// readLocalConfigValue returns the current --local value for a key, or
// the empty string if it isn't set. `git config --get` exits non-zero
// when the key is absent — that's normal here, not an error to surface.
func readLocalConfigValue(repoRoot, key string) string {
	out, err := exec.Command("git", "-C", repoRoot, "config", "--local", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeLocalConfigValue sets a --local config key to value via shell-out.
// Matches the read pattern in the v0.7.1 Repo Config view; avoids the
// risk of go-git's config marshaller reordering keys or losing comments
// on a round-trip.
func writeLocalConfigValue(repoRoot, key, value string) error {
	cmd := exec.Command("git", "-C", repoRoot, "config", "--local", key, value)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────
// Manage Remotes (v0.8.0)
//
// Add / edit-URL / remove for the repo's configured remotes, plus a small
// Build URL helper that constructs a git URL from a host + path and can
// optionally probe both HTTPS and SSH variants in parallel to find the
// working one. Strictly host-agnostic — no baked-in knowledge of any
// public or internal git server. The global url.<base>.insteadof rewrite
// machinery handles host-level policy; this dialog just stores whatever
// URL the user chooses.
// ─────────────────────────────────────────────────────────────────────────

// gitProbeTimeout caps each ls-remote probe in the Build URL helper and
// the Test Connection button. 5s matches the v0.5.0 remote-sync indicator
// — long enough for slow networks, short enough that a hung HTTPS request
// doesn't leave the UI feeling stuck.
const gitProbeTimeout = 5 * time.Second

// httpsURLRe matches the canonical https://<host>/<path>[.git] form.
// http:// is accepted too for self-hosted git servers without TLS.
var httpsURLRe = regexp.MustCompile(`^https?://([^/]+)/(.+?)(\.git)?$`)

// sshURLRe matches the standard scp-like SSH form: <user>@<host>:<path>[.git].
// The less-common ssh://user@host:port/path form is not matched here —
// users who paste that get raw-URL behaviour without form conversion,
// which is fine.
var sshURLRe = regexp.MustCompile(`^([\w.-]+)@([\w.-]+):(.+?)(\.git)?$`)

// parseGitURL decomposes a URL into its parts and tells you which form
// (HTTPS / SSH) it's in. Returns ok=false for anything that doesn't match
// the two canonical forms; the caller should then treat the URL as
// opaque (no form toggle, no automatic conversion).
func parseGitURL(url string) (host, path, scheme string, hasGitSuffix, ok bool) {
	url = strings.TrimSpace(url)
	if m := httpsURLRe.FindStringSubmatch(url); m != nil {
		return m[1], m[2], "HTTPS", m[3] == ".git", true
	}
	if m := sshURLRe.FindStringSubmatch(url); m != nil {
		return m[2], m[3], "SSH", m[4] == ".git", true
	}
	return "", "", "", false, false
}

// convertURLForm flips an https://… URL to its git@host:… SSH equivalent
// (or vice versa), preserving the `.git` suffix iff present in the input.
// Returns ok=false when the input isn't a recognised form; the caller
// should leave the URL untouched in that case.
func convertURLForm(url string) (other string, ok bool) {
	host, path, scheme, hasGit, parseOK := parseGitURL(url)
	if !parseOK {
		return "", false
	}
	suffix := ""
	if hasGit {
		suffix = ".git"
	}
	switch scheme {
	case "HTTPS":
		return fmt.Sprintf("git@%s:%s%s", host, path, suffix), true
	case "SSH":
		return fmt.Sprintf("https://%s/%s%s", host, path, suffix), true
	}
	return "", false
}

// buildGitURL constructs a git URL from raw parts. Path is taken
// verbatim, so the caller decides whether to include `.git` — convention
// is to let the user paste/type the path as they would see it on the
// host's web UI; gitservers accept both `<path>` and `<path>.git`.
func buildGitURL(host, path, scheme string) string {
	host = strings.TrimSpace(host)
	path = strings.TrimSpace(strings.TrimPrefix(path, "/"))
	switch strings.ToUpper(scheme) {
	case "HTTPS":
		return fmt.Sprintf("https://%s/%s", host, path)
	case "SSH":
		return fmt.Sprintf("git@%s:%s", host, path)
	}
	return ""
}

// probeRemoteURL runs `git ls-remote --heads <url>` with a context timeout
// and returns nil on success or git's verbatim stderr on failure. Used by
// both the Build URL helper (auto-detect mode) and the Test Connection
// button on the Add/Edit dialog.
func probeRemoteURL(ctx context.Context, url string) error {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// probeBothSchemes fires the HTTPS and SSH probes concurrently, waits
// for both, and picks a winner. SSH wins on ties (Allan's stated
// preference and what the typical `url.insteadof` rewrite for an
// HTTPS-flaky host would yield anyway). Both errors are returned so the
// caller can show side-by-side diagnostics on total failure.
//
// Wall clock is bounded at gitProbeTimeout (5s) since both probes share
// the same context. The "fast scheme succeeds quickly, slow one hangs
// for 5s" case still has to wait for the timeout — a race-cancel design
// would shave that latency but at the cost of losing the both-succeed
// tie-break. The simple wait-for-both approach is correct and the
// worst-case wait is the same as the user's existing CLI experience.
func probeBothSchemes(httpsURL, sshURL string) (preferredURL string, httpsErr, sshErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitProbeTimeout)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		httpsErr = probeRemoteURL(ctx, httpsURL)
	}()
	go func() {
		defer wg.Done()
		sshErr = probeRemoteURL(ctx, sshURL)
	}()
	wg.Wait()

	switch {
	case sshErr == nil:
		return sshURL, httpsErr, sshErr
	case httpsErr == nil:
		return httpsURL, httpsErr, sshErr
	default:
		return "", httpsErr, sshErr
	}
}

// remoteEntry is one row in the Manage Remotes dialog's list.
type remoteEntry struct {
	name string
	urls []string // a remote can have multiple URLs (e.g. separate fetch/push); each is its own row in the underlying refRow rendering, but for management we treat the *primary* URL as the editable one
}

// listRemoteEntries returns the repo's configured remotes, sorted by
// name. Built on go-git's Remotes() so it matches the read shown by the
// existing read-only View ▾ → Remotes… dialog.
func listRemoteEntries(repo *git.Repository) ([]remoteEntry, error) {
	rs, err := repo.Remotes()
	if err != nil {
		return nil, err
	}
	out := make([]remoteEntry, 0, len(rs))
	for _, r := range rs {
		cfg := r.Config()
		if cfg == nil {
			continue
		}
		out = append(out, remoteEntry{name: cfg.Name, urls: append([]string(nil), cfg.URLs...)})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].name) < strings.ToLower(out[j].name) })
	return out, nil
}

// addRemote / removeRemote / setRemoteURL shell out to git rather than
// using go-git's API — keeps the on-disk .git/config formatting
// identical to what the user would produce by hand, and avoids any
// risk of go-git's config marshaller reordering keys or losing comments.
func addRemote(repoRoot, name, url string) error {
	cmd := exec.Command("git", "-C", repoRoot, "remote", "add", name, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeRemote(repoRoot, name string) error {
	cmd := exec.Command("git", "-C", repoRoot, "remote", "remove", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func setRemoteURL(repoRoot, name, url string) error {
	cmd := exec.Command("git", "-C", repoRoot, "remote", "set-url", name, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// showManageRemotesDialog is the entry point for the Repo ▾ → Manage
// Remotes… menu item. Shows the list of configured remotes with
// Add / Edit / Remove buttons, all read+write via the helpers above.
// The onChanged callback fires after each successful mutation so the
// caller (the explorer view) can refresh derived UI like the header's
// remote-sync indicator.
func showManageRemotesDialog(parent fyne.Window, repo *git.Repository, repoRoot string, onChanged func()) {
	if repo == nil || repoRoot == "" {
		return
	}

	entries, err := listRemoteEntries(repo)
	if err != nil {
		dialog.ShowError(fmt.Errorf("list remotes: %w", err), parent)
		return
	}

	selected := -1
	list := widget.NewList(
		func() int { return len(entries) },
		func() fyne.CanvasObject { return newRefRowWidget(2) },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(entries) {
				return
			}
			row := o.(*refRowWidget)
			e := entries[id]
			row.name.SetText(e.name)
			joined := strings.Join(e.urls, "  /  ")
			if joined == "" {
				joined = "(no URLs)"
			}
			row.wide.SetText(joined)
		},
	)

	var d dialog.Dialog
	var addBtn, editBtn, removeBtn *widget.Button

	reload := func() {
		entries, _ = listRemoteEntries(repo)
		selected = -1
		list.UnselectAll()
		list.Refresh()
		editBtn.Disable()
		removeBtn.Disable()
		if onChanged != nil {
			onChanged()
		}
	}

	list.OnSelected = func(id widget.ListItemID) {
		selected = id
		editBtn.Enable()
		removeBtn.Enable()
	}
	list.OnUnselected = func(id widget.ListItemID) {
		selected = -1
		editBtn.Disable()
		removeBtn.Disable()
	}

	addBtn = widget.NewButtonWithIcon("Add…", theme.ContentAddIcon(), func() {
		showAddOrEditRemoteDialog(parent, repoRoot, nil, reload)
	})
	editBtn = widget.NewButtonWithIcon("Edit…", theme.DocumentIcon(), func() {
		if selected < 0 || selected >= len(entries) {
			return
		}
		e := entries[selected]
		seedURL := ""
		if len(e.urls) > 0 {
			seedURL = e.urls[0]
		}
		showAddOrEditRemoteDialog(parent, repoRoot, &remoteEntry{name: e.name, urls: []string{seedURL}}, reload)
	})
	removeBtn = widget.NewButtonWithIcon("Remove…", theme.DeleteIcon(), func() {
		if selected < 0 || selected >= len(entries) {
			return
		}
		e := entries[selected]
		dialog.ShowConfirm("Remove remote",
			fmt.Sprintf("Remove the remote %q?\n\nThis only deletes the remote entry in .git/config — no data on the server is touched.", e.name),
			func(confirmed bool) {
				if !confirmed {
					return
				}
				if err := removeRemote(repoRoot, e.name); err != nil {
					dialog.ShowError(fmt.Errorf("remove remote: %w", err), parent)
					return
				}
				reload()
			}, parent)
	})
	editBtn.Disable()
	removeBtn.Disable()

	intro := widget.NewLabel("Add, edit, or remove the URLs git uses for fetch/push. URLs are stored verbatim — any url.<base>.insteadof rewrite in your global config still applies at fetch/push time.")
	intro.Wrapping = fyne.TextWrapWord
	intro.TextStyle = fyne.TextStyle{Italic: true}

	buttons := container.NewHBox(addBtn, editBtn, removeBtn)
	body := container.NewBorder(
		container.NewVBox(intro, widget.NewSeparator()),
		buttons,
		nil, nil,
		list,
	)
	body.Resize(fyne.NewSize(680, 420))

	d = dialog.NewCustom("Manage Remotes — "+strings.TrimSpace(repoRoot), "Close", body, parent)
	d.Resize(fyne.NewSize(780, 480))
	d.Show()
}

// showAddOrEditRemoteDialog presents the single Add/Edit sub-dialog for
// a remote. When existing == nil, this is an Add flow (Name field
// editable). When existing != nil, this is an Edit flow (Name field
// disabled — renaming a remote needs `git remote rename` which is a
// separate operation we don't surface yet; users can Remove + Add if
// they want a rename).
func showAddOrEditRemoteDialog(parent fyne.Window, repoRoot string, existing *remoteEntry, onSaved func()) {
	isEdit := existing != nil

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("e.g. origin")
	if isEdit {
		nameEntry.SetText(existing.name)
		nameEntry.Disable()
	}

	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("e.g. git@github.com:USERNAME/PROJECT.git or https://github.com/USERNAME/PROJECT.git")
	if isEdit && len(existing.urls) > 0 {
		urlEntry.SetText(existing.urls[0])
	}

	// Form toggle: visible only when the current URL is a parseable
	// HTTPS or SSH form; the button label reflects the *other* form.
	flipBtn := widget.NewButton("Flip to …", nil)
	flipBtn.Hide()
	updateFlipBtn := func() {
		other, ok := convertURLForm(urlEntry.Text)
		if !ok {
			flipBtn.Hide()
			return
		}
		_, _, scheme, _, _ := parseGitURL(urlEntry.Text)
		switch scheme {
		case "HTTPS":
			flipBtn.SetText("Flip to SSH form")
		case "SSH":
			flipBtn.SetText("Flip to HTTPS form")
		}
		flipBtn.OnTapped = func() {
			urlEntry.SetText(other)
		}
		flipBtn.Show()
	}
	urlEntry.OnChanged = func(string) { updateFlipBtn() }
	updateFlipBtn()

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	buildBtn := widget.NewButton("Build URL from host + path…", func() {
		showBuildURLHelperDialog(parent, urlEntry.Text, func(built string) {
			urlEntry.SetText(built)
		})
	})

	tryOtherBtn := widget.NewButton("Try as other form", nil)
	tryOtherBtn.Hide()

	testBtn := widget.NewButton("Test connection", func() {
		url := strings.TrimSpace(urlEntry.Text)
		if url == "" {
			statusLabel.SetText("Enter a URL first.")
			tryOtherBtn.Hide()
			return
		}
		statusLabel.SetText("Probing…")
		tryOtherBtn.Hide()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), gitProbeTimeout)
			defer cancel()
			err := probeRemoteURL(ctx, url)
			fyne.Do(func() {
				if err == nil {
					statusLabel.SetText("✓ Reachable.")
					tryOtherBtn.Hide()
					return
				}
				statusLabel.SetText("✗ " + err.Error())
				// Offer the one-click flip when the URL has a recognised
				// form — common rescue when one scheme is flaky on a
				// host but the other works.
				if other, ok := convertURLForm(url); ok {
					tryOtherBtn.SetText("Retry test with other form")
					tryOtherBtn.OnTapped = func() {
						urlEntry.SetText(other)
						tryOtherBtn.Hide()
						statusLabel.SetText("Probing…")
						go func() {
							ctx2, cancel2 := context.WithTimeout(context.Background(), gitProbeTimeout)
							defer cancel2()
							err2 := probeRemoteURL(ctx2, other)
							fyne.Do(func() {
								if err2 == nil {
									statusLabel.SetText("✓ Reachable (other form).")
								} else {
									statusLabel.SetText("✗ " + err2.Error())
								}
							})
						}()
					}
					tryOtherBtn.Show()
				}
			})
		}()
	})

	form := widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("URL", urlEntry),
	)
	helperRow := container.NewHBox(flipBtn, buildBtn, testBtn, tryOtherBtn)
	content := container.NewVBox(form, helperRow, statusLabel)

	title := "Add Remote"
	saveLabel := "Add"
	if isEdit {
		title = "Edit Remote — " + existing.name
		saveLabel = "Save"
	}

	d := dialog.NewCustomConfirm(title, saveLabel, "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		name := strings.TrimSpace(nameEntry.Text)
		url := strings.TrimSpace(urlEntry.Text)
		if name == "" {
			dialog.ShowError(fmt.Errorf("remote name is required"), parent)
			return
		}
		if url == "" {
			dialog.ShowError(fmt.Errorf("URL is required"), parent)
			return
		}
		if isEdit {
			if err := setRemoteURL(repoRoot, name, url); err != nil {
				dialog.ShowError(fmt.Errorf("set URL: %w", err), parent)
				return
			}
		} else {
			if err := addRemote(repoRoot, name, url); err != nil {
				dialog.ShowError(fmt.Errorf("add remote: %w", err), parent)
				return
			}
		}
		if onSaved != nil {
			onSaved()
		}
	}, parent)
	d.Resize(fyne.NewSize(680, 320))
	d.Show()
}

// showBuildURLHelperDialog is the small sub-dialog reachable from the
// Add/Edit Remote dialog's "Build URL from host + path…" button. Takes
// host + path + scheme inputs and produces a single URL string passed
// back to onBuilt. Scheme "Auto-detect" fires both probes concurrently
// (via probeBothSchemes) and uses whichever responds successfully,
// preferring SSH on ties.
func showBuildURLHelperDialog(parent fyne.Window, seedURL string, onBuilt func(string)) {
	hostEntry := widget.NewEntry()
	hostEntry.SetPlaceHolder("e.g. github.com")
	pathEntry := widget.NewEntry()
	pathEntry.SetPlaceHolder("e.g. USERNAME/PROJECT.git  (the .git suffix is the conventional form; bare USERNAME/PROJECT also works)")

	// Pre-fill from a parseable seed URL so re-opening the helper while
	// editing an existing remote starts at sensible values.
	if seedURL != "" {
		if host, path, _, _, ok := parseGitURL(seedURL); ok {
			hostEntry.SetText(host)
			pathEntry.SetText(path)
		}
	}

	autoRadio := widget.NewRadioGroup([]string{"Auto-detect (probe HTTPS and SSH)", "HTTPS only", "SSH only"}, nil)
	autoRadio.SetSelected("Auto-detect (probe HTTPS and SSH)")

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	// `built` survives the dialog callback closure so the OK path knows
	// whether [Build] has been clicked. Without it, the Use this URL
	// button would have nothing to hand back.
	var built string

	buildBtn := widget.NewButton("Build URL", nil)
	buildBtn.OnTapped = func() {
		host := strings.TrimSpace(hostEntry.Text)
		path := strings.TrimSpace(pathEntry.Text)
		if host == "" || path == "" {
			statusLabel.SetText("Enter both host and path first.")
			return
		}
		switch autoRadio.Selected {
		case "HTTPS only":
			built = buildGitURL(host, path, "HTTPS")
			statusLabel.SetText("Built: " + built)
		case "SSH only":
			built = buildGitURL(host, path, "SSH")
			statusLabel.SetText("Built: " + built)
		default: // Auto-detect
			httpsURL := buildGitURL(host, path, "HTTPS")
			sshURL := buildGitURL(host, path, "SSH")
			statusLabel.SetText("Probing HTTPS and SSH in parallel (up to 5s)…")
			buildBtn.Disable()
			go func() {
				winner, hErr, sErr := probeBothSchemes(httpsURL, sshURL)
				fyne.Do(func() {
					buildBtn.Enable()
					if winner != "" {
						built = winner
						msg := "✓ Picked "
						if winner == sshURL {
							msg += "SSH"
						} else {
							msg += "HTTPS"
						}
						msg += ": " + winner
						if hErr == nil && sErr == nil {
							msg += "  (both reachable; SSH preferred on tie)"
						}
						statusLabel.SetText(msg)
						return
					}
					// Both failed — show both errors so the user can see
					// which one is rescuable.
					built = ""
					statusLabel.SetText(fmt.Sprintf("✗ Neither scheme reached the host.\n   HTTPS: %s\n   SSH:   %s", hErr, sErr))
				})
			}()
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("Host", hostEntry),
		widget.NewFormItem("Path", pathEntry),
		widget.NewFormItem("Scheme", autoRadio),
	)
	content := container.NewVBox(form, buildBtn, statusLabel)

	d := dialog.NewCustomConfirm("Build URL from host + path", "Use this URL", "Cancel",
		content, func(ok bool) {
			if !ok {
				return
			}
			if built == "" {
				dialog.ShowError(fmt.Errorf("click Build URL first to construct (and optionally probe) the URL"), parent)
				return
			}
			if onBuilt != nil {
				onBuilt(built)
			}
		}, parent)
	d.Resize(fyne.NewSize(640, 360))
	d.Show()
}

// ─────────────────────────────────────────────────────────────────────────
// Commit (v0.8.0)
//
// "Type a message, commit what's staged" — plus two ergonomic
// extensions both off by default: Stage all changes (git add -A first),
// and Amend last commit (git commit --amend, keeping the previous
// message if the field is left blank). GPG signing is delegated to
// git's config (commit.gpgsign); we don't expose a per-commit signing
// toggle. Sign-off (-s) is intentionally out of scope.
// ─────────────────────────────────────────────────────────────────────────

// stagedFilesSummary returns a short one-line description of what's
// currently in the index, e.g. "3 files staged" or "Nothing staged".
// Used by the Commit dialog so the user sees what they're about to
// commit without having to swap windows.
func stagedFilesSummary(repoRoot string) (count int, sample []string) {
	out, err := exec.Command("git", "-C", repoRoot, "diff", "--cached", "--name-only").Output()
	if err != nil {
		return 0, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		count++
		if len(sample) < 5 {
			sample = append(sample, line)
		}
	}
	return count, sample
}

// previousCommitMessage returns the full message body of HEAD, used to
// pre-fill the message field when Amend is ticked. Empty string when
// HEAD is unborn (freshly-init'd repo with no commits yet) or anything
// else goes wrong — the dialog treats empty as "no seed, start fresh".
func previousCommitMessage(repoRoot string) string {
	out, err := exec.Command("git", "-C", repoRoot, "log", "-1", "--format=%B").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// hasAnyCommits reports whether HEAD resolves to a commit. False on a
// freshly-init'd repo where HEAD is an unborn symref. Amend is hidden
// in that case — there's nothing to amend.
func hasAnyCommits(repoRoot string) bool {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "HEAD")
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// hasAnyChanges reports whether there's anything for "Stage all" to do —
// either modified-but-not-staged files or untracked files. Used to
// pre-validate the dialog before running git commit and producing a
// confusing "nothing to commit" error.
func hasAnyChanges(repoRoot string) bool {
	out, err := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// runCommit invokes git commit with the right flag mix. Stage-all
// happens as a separate `git add -A` step so we can surface its error
// distinctly from the commit step's error. Amend mode falls back to
// --no-edit when the message is empty (preserves the previous message
// — the most common amend pattern, "I forgot something").
func runCommit(repoRoot, message string, stageAll, amend bool) (sha string, err error) {
	if stageAll {
		cmd := exec.Command("git", "-C", repoRoot, "add", "-A")
		if out, e := cmd.CombinedOutput(); e != nil {
			return "", fmt.Errorf("stage all: %w\n%s", e, strings.TrimSpace(string(out)))
		}
	}
	args := []string{"-C", repoRoot, "commit"}
	if amend {
		args = append(args, "--amend")
		if strings.TrimSpace(message) == "" {
			args = append(args, "--no-edit")
		} else {
			args = append(args, "-m", message)
		}
	} else {
		if strings.TrimSpace(message) == "" {
			return "", fmt.Errorf("commit message is required")
		}
		args = append(args, "-m", message)
	}
	cmd := exec.Command("git", args...)
	if out, e := cmd.CombinedOutput(); e != nil {
		return "", fmt.Errorf("%w\n%s", e, strings.TrimSpace(string(out)))
	}
	// Read back the resulting commit SHA so the success dialog can
	// report it. rev-parse is cheap; failure here is non-fatal.
	if out, e := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD").Output(); e == nil {
		sha = strings.TrimSpace(string(out))
	}
	return sha, nil
}

// showCommitDialog opens the commit composer. After a successful commit
// the supplied onCommitted callback fires — typically a view refresh so
// the explorer's clean/dirty counts, header last-commit line, and
// remote-sync indicator update without the user manually refreshing.
func showCommitDialog(parent fyne.Window, repoRoot string, onCommitted func()) {
	if repoRoot == "" {
		dialog.ShowInformation("No repository",
			"Open a folder that is inside a git repository first, then try again.",
			parent)
		return
	}

	messageEntry := widget.NewMultiLineEntry()
	messageEntry.SetPlaceHolder("Commit message (first line is the subject; leave a blank line before the body)")
	messageEntry.SetMinRowsVisible(5)

	stageAllCheck := widget.NewCheck("Stage all changes before committing (git add -A)", nil)

	amendCheck := widget.NewCheck("Amend last commit (overwrite the previous commit with these changes)", nil)
	amendCheck.OnChanged = func(checked bool) {
		if checked {
			// Pre-fill with HEAD's message so the user can edit it
			// in-place; blank field on save still works (uses
			// --no-edit) for the pure "fix the staged content" amend.
			if messageEntry.Text == "" {
				messageEntry.SetText(previousCommitMessage(repoRoot))
			}
		}
	}

	if !hasAnyCommits(repoRoot) {
		// First commit ever — Amend has nothing to amend.
		amendCheck.Disable()
	}

	// Staged-files preview — one short line plus up to 5 filenames so
	// the user sees what's going in without swapping windows.
	previewLabel := widget.NewLabel("")
	previewLabel.Wrapping = fyne.TextWrapWord
	previewLabel.TextStyle = fyne.TextStyle{Italic: true}
	refreshPreview := func() {
		count, sample := stagedFilesSummary(repoRoot)
		if count == 0 {
			if hasAnyChanges(repoRoot) {
				previewLabel.SetText("Nothing staged yet. Tick \"Stage all changes\" below to include everything, or stage individual files via the file list's right-click menu.")
			} else {
				previewLabel.SetText("Nothing staged and no changes in the working tree.")
			}
			return
		}
		text := fmt.Sprintf("Staged: %d file(s)", count)
		if len(sample) > 0 {
			text += " — " + strings.Join(sample, ", ")
			if count > len(sample) {
				text += fmt.Sprintf(", + %d more", count-len(sample))
			}
		}
		previewLabel.SetText(text)
	}
	refreshPreview()

	stageAllCheck.OnChanged = func(checked bool) {
		// When stage-all is on, the preview's "Nothing staged" warning
		// becomes misleading — show what would be staged instead.
		if checked {
			out, err := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
			if err == nil {
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				count := 0
				for _, l := range lines {
					if strings.TrimSpace(l) != "" {
						count++
					}
				}
				if count > 0 {
					previewLabel.SetText(fmt.Sprintf("Stage all → will stage %d file(s) before committing.", count))
					return
				}
			}
		}
		refreshPreview()
	}

	content := container.NewVBox(
		previewLabel,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Message", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		messageEntry,
		stageAllCheck,
		amendCheck,
	)

	d := dialog.NewCustomConfirm("Commit", "Commit", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		// Pre-validate: non-amend with nothing staged AND no stage-all
		// ticked means git commit would just fail with "nothing to
		// commit". Catch it early with a clearer message.
		if !amendCheck.Checked {
			count, _ := stagedFilesSummary(repoRoot)
			if count == 0 && !stageAllCheck.Checked {
				dialog.ShowError(fmt.Errorf("nothing staged. Either stage files first (right-click in the file list), or tick \"Stage all changes\""), parent)
				return
			}
		}
		sha, err := runCommit(repoRoot, messageEntry.Text, stageAllCheck.Checked, amendCheck.Checked)
		if err != nil {
			dialog.ShowError(fmt.Errorf("commit: %w", err), parent)
			return
		}
		summary := "Commit created"
		if amendCheck.Checked {
			summary = "Commit amended"
		}
		if sha != "" {
			summary += " — " + sha
		}
		dialog.ShowInformation(summary,
			"The explorer view will refresh to show the updated repo state.",
			parent)
		if onCommitted != nil {
			onCommitted()
		}
	}, parent)
	d.Resize(fyne.NewSize(680, 480))
	d.Show()
}

// ─────────────────────────────────────────────────────────────────────────
// Push-time helpers: remote-side message parsing + browser open
//
// GitHub injects `remote:` lines into push output for things like
// Dependabot vulnerability summaries. Surfacing these inline turns a
// passive "check your email later" signal into an actionable one. The
// parser is deliberately scoped to a single pattern for now — the
// vulnerability headline — and easy to extend if more remote-side
// messages prove useful.
// ─────────────────────────────────────────────────────────────────────────

// vulnAlert is the parsed bundle of GitHub's "GitHub found N
// vulnerabilities …" remote message. severity is the parenthesised
// breakdown ("2 moderate, 1 low"); empty if GitHub didn't include one.
// url is the dependabot page link from the following `remote:` line.
type vulnAlert struct {
	count    int
	severity string
	url      string
}

// vulnHeadlineRe matches GitHub's vulnerability summary line in push
// output. The phrasing is stable across the GitHub fleet so a single
// regex covers it. The severity capture is optional — older repos
// without a severity breakdown still match.
var vulnHeadlineRe = regexp.MustCompile(`GitHub found (\d+) vulnerabilit(?:y|ies)[^(]*(?:\(([^)]+)\))?`)

// vulnURLRe extracts the dependabot URL GitHub prints on the line
// after the headline. Scoped to /security/dependabot paths so a stray
// URL elsewhere in the output doesn't get mistaken for it.
var vulnURLRe = regexp.MustCompile(`(https?://\S+/security/dependabot)`)

// parseVulnAlert scans push output for GitHub's Dependabot summary.
// Returns nil when the headline isn't present — silent success is the
// correct UX (no noise when there's nothing actionable).
func parseVulnAlert(output string) *vulnAlert {
	m := vulnHeadlineRe.FindStringSubmatch(output)
	if m == nil {
		return nil
	}
	count, _ := strconv.Atoi(m[1])
	alert := &vulnAlert{count: count, severity: strings.TrimSpace(m[2])}
	if u := vulnURLRe.FindStringSubmatch(output); u != nil {
		alert.url = u[1]
	}
	return alert
}

// dependabotAlert is the slimmed-down per-alert struct the Fetch
// Alert Details dialog renders. Sourced from `gh api
// repos/<o>/<r>/dependabot/alerts?state=open` — we only deserialise the
// fields the UI actually shows, leaving GitHub's full payload alone.
type dependabotAlert struct {
	Number       int
	Severity     string // critical | high | medium | low (GitHub UI shows "moderate" for "medium")
	PackageName  string
	Ecosystem    string
	Relationship string // direct | transitive
	Summary      string
	GHSAID       string
	CVEID        string
	FixedVersion string
	VulnRange    string
	HTMLURL      string
}

// dependabotURLRe extracts owner/repo from a /security/dependabot URL.
// Allows an optional trailing slash; doesn't require https because the
// URL git prints in push output sometimes lacks the scheme.
var dependabotURLRe = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/security/dependabot/?$`)

// parseDependabotURL pulls owner + repo out of the URL GitHub prints
// after a vuln-finding push. Returns ok=false for any URL that doesn't
// match the canonical github.com/<owner>/<repo>/security/dependabot
// shape — keeps the auto-fetch button disabled rather than firing a
// request against an unrelated host.
func parseDependabotURL(url string) (owner, repo string, ok bool) {
	m := dependabotURLRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// severityRank maps GitHub's severity strings to a sortable integer so
// the alerts dialog can lead with the worst. Unknown / blank severities
// sink to the bottom.
func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium", "moderate":
		return 2
	case "low":
		return 3
	}
	return 4
}

// fetchDependabotAlerts shells to `gh api` to retrieve open Dependabot
// alerts for the repo. Requires the `gh` CLI installed and
// authenticated; both failures get a specific actionable hint rather
// than git's raw stderr. Sorted critical-first.
func fetchDependabotAlerts(ctx context.Context, host, owner, repo string) ([]dependabotAlert, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/ to enable Dependabot alert auto-fetch (or use Open Dependabot alerts to view in your browser)")
	}
	endpoint := fmt.Sprintf("repos/%s/%s/dependabot/alerts?state=open", owner, repo)
	args := []string{"api", endpoint}
	// Route to a GitHub Enterprise Server instance when the repo isn't on
	// github.com; gh's --hostname picks the right host's stored credentials.
	if host != "" && !strings.EqualFold(host, "github.com") {
		args = append(args, "--hostname", host)
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		stderr := strings.TrimSpace(string(out))
		// Detect the common "not authenticated" case so we can offer
		// the right hint without making the user parse gh's verbose
		// HTTP-401 dump.
		if strings.Contains(stderr, "Bad credentials") || strings.Contains(stderr, "authentication") || strings.Contains(stderr, "401") {
			return nil, fmt.Errorf("gh CLI is not authenticated — run `gh auth login` in a terminal, then retry")
		}
		return nil, fmt.Errorf("gh api: %w\n%s", err, stderr)
	}
	// Raw shape mirrors GitHub's REST response; only the fields the
	// UI surfaces are picked out below.
	var raw []struct {
		Number     int `json:"number"`
		Dependency struct {
			Package struct {
				Ecosystem string `json:"ecosystem"`
				Name      string `json:"name"`
			} `json:"package"`
			Relationship string `json:"relationship"`
		} `json:"dependency"`
		SecurityAdvisory struct {
			Summary  string `json:"summary"`
			Severity string `json:"severity"`
			GHSAID   string `json:"ghsa_id"`
			CVEID    string `json:"cve_id"`
		} `json:"security_advisory"`
		SecurityVulnerability struct {
			VulnerableVersionRange string `json:"vulnerable_version_range"`
			FirstPatchedVersion    *struct {
				Identifier string `json:"identifier"`
			} `json:"first_patched_version"`
		} `json:"security_vulnerability"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse gh response: %w", err)
	}
	alerts := make([]dependabotAlert, 0, len(raw))
	for _, r := range raw {
		a := dependabotAlert{
			Number:       r.Number,
			Severity:     r.SecurityAdvisory.Severity,
			PackageName:  r.Dependency.Package.Name,
			Ecosystem:    r.Dependency.Package.Ecosystem,
			Relationship: r.Dependency.Relationship,
			Summary:      r.SecurityAdvisory.Summary,
			GHSAID:       r.SecurityAdvisory.GHSAID,
			CVEID:        r.SecurityAdvisory.CVEID,
			VulnRange:    r.SecurityVulnerability.VulnerableVersionRange,
			HTMLURL:      r.HTMLURL,
		}
		if r.SecurityVulnerability.FirstPatchedVersion != nil {
			a.FixedVersion = r.SecurityVulnerability.FirstPatchedVersion.Identifier
		}
		alerts = append(alerts, a)
	}
	sort.SliceStable(alerts, func(i, j int) bool {
		ri, rj := severityRank(alerts[i].Severity), severityRank(alerts[j].Severity)
		if ri != rj {
			return ri < rj
		}
		return alerts[i].Number > alerts[j].Number // newest first within a severity
	})
	return alerts, nil
}

// applyGoFix runs `go get <pkg>@<version>` in repoRoot, returning git's
// combined output for display. The version is taken verbatim — the
// caller is responsible for the leading-v normalisation. `go get` is
// the canonical way to bump a Go-module dep; it works for both direct
// and transitive entries (forcing a transitive into go.mod as an
// explicit `require` line if it wasn't already).
func applyGoFix(repoRoot, pkg, version string) (output string, err error) {
	if _, e := exec.LookPath("go"); e != nil {
		return "", fmt.Errorf("go toolchain not found on PATH — install Go from https://go.dev/dl/ to enable auto-fix")
	}
	cmd := exec.Command("go", "get", pkg+"@"+version)
	cmd.Dir = repoRoot
	out, e := cmd.CombinedOutput()
	if e != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%w\n%s", e, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// runGoModTidy runs `go mod tidy` in repoRoot. Called after auto-fix
// applies so go.mod / go.sum reflect the deduped, minimal state — the
// way the user's editor / CI / dependabot would expect to see it.
func runGoModTidy(repoRoot string) (output string, err error) {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = repoRoot
	out, e := cmd.CombinedOutput()
	if e != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%w\n%s", e, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// showDependabotAlertsDialog renders the fetched alerts as a scrollable
// list of one-card-per-alert. Each card shows severity badge, package +
// ecosystem + direct/transitive, the advisory summary, the vulnerable
// range + fixed version, and an "Open advisory" button.
//
// When the alert is for a Go module AND has a known fixed version, a
// second per-card button — "Apply fix (go get)" — appears next to
// Open advisory. Clicking it confirms, then runs `go get
// pkg@version` followed by `go mod tidy` in repoRoot. No button when
// no fix exists yet (advisory acknowledged but unpatched), and no
// button for non-Go ecosystems (npm/pip/etc would need their own
// updater plumbing).
func showDependabotAlertsDialog(parent fyne.Window, owner, repo, repoRoot string, alerts []dependabotAlert) {
	title := fmt.Sprintf("Dependabot alerts — %s/%s (%d open)", owner, repo, len(alerts))
	if len(alerts) == 0 {
		dialog.ShowInformation(title,
			"No open Dependabot alerts. (You may have dismissed or fixed them since the push that triggered this view.)",
			parent)
		return
	}

	cards := container.NewVBox()
	for _, a := range alerts {
		alert := a // capture for closure

		severityText := strings.Title(alert.Severity)
		if strings.ToLower(alert.Severity) == "medium" {
			severityText = "Moderate" // match GitHub UI's wording so users see the same label
		}
		severityBadge := widget.NewLabelWithStyle(severityText, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

		pkgText := alert.PackageName
		if alert.Ecosystem != "" {
			pkgText += "  (" + alert.Ecosystem + ", " + alert.Relationship + ")"
		}
		pkgLabel := widget.NewLabelWithStyle(pkgText, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true, Bold: true})
		pkgLabel.Wrapping = fyne.TextWrapWord

		summaryLabel := widget.NewLabel(alert.Summary)
		summaryLabel.Wrapping = fyne.TextWrapWord

		var meta []string
		if alert.VulnRange != "" {
			meta = append(meta, "Vulnerable: "+alert.VulnRange)
		}
		if alert.FixedVersion != "" {
			meta = append(meta, "Fixed in: "+alert.FixedVersion)
		}
		if alert.CVEID != "" {
			meta = append(meta, alert.CVEID)
		}
		if alert.GHSAID != "" {
			meta = append(meta, alert.GHSAID)
		}
		metaLabel := widget.NewLabel(strings.Join(meta, "   ·   "))
		metaLabel.TextStyle = fyne.TextStyle{Italic: true}
		metaLabel.Wrapping = fyne.TextWrapWord

		openBtn := widget.NewButtonWithIcon("Open advisory", theme.ComputerIcon(), func() {
			if e := openURLInBrowser(alert.HTMLURL); e != nil {
				dialog.ShowError(fmt.Errorf("open browser: %w", e), parent)
			}
		})

		// Per-row buttons live in a right-side HBox. Apply-fix joins
		// Open-advisory only when this alert is actionable via `go
		// get` — ecosystem == "go" AND a known fixed version. Both
		// gates matter: an advisory without a published fix
		// (FixedVersion == "") has nothing for us to call yet.
		// repoRoot guard: the aggregate Dependabot view reuses this dialog
		// for repos that aren't cloned locally (repoRoot == ""), where there's
		// no working tree to run `go get` against — those render read-only.
		buttons := container.NewHBox(openBtn)
		if repoRoot != "" && strings.EqualFold(alert.Ecosystem, "go") && alert.FixedVersion != "" {
			fixVersion := alert.FixedVersion
			if !strings.HasPrefix(fixVersion, "v") {
				fixVersion = "v" + fixVersion
			}
			applyBtn := widget.NewButtonWithIcon("Apply fix (go get)", theme.MoveUpIcon(), nil)
			applyBtn.Importance = widget.HighImportance
			applyBtn.OnTapped = func() {
				dialog.ShowConfirm("Apply fix?",
					fmt.Sprintf("Run:\n    go get %s@%s\n    go mod tidy\n\nin %s.\n\nThis updates go.mod and go.sum. You'll need to commit the changes to publish the fix.\n\nContinue?",
						alert.PackageName, fixVersion, repoRoot),
					func(yes bool) {
						if !yes {
							return
						}
						applyBtn.Disable()
						origText := applyBtn.Text
						applyBtn.SetText("Applying…")
						go func() {
							getOut, getErr := applyGoFix(repoRoot, alert.PackageName, fixVersion)
							var tidyOut string
							var tidyErr error
							if getErr == nil {
								tidyOut, tidyErr = runGoModTidy(repoRoot)
							}
							fyne.Do(func() {
								if getErr != nil {
									// Re-enable for retry on failure — the
									// upgrade didn't land, so the user
									// might want to try again after
									// fixing the root cause (network,
									// auth, conflicting require, etc.).
									applyBtn.Enable()
									applyBtn.SetText(origText)
									dialog.ShowError(fmt.Errorf("go get failed:\n%w\n\n%s", getErr, getOut), parent)
									return
								}
								if tidyErr != nil {
									applyBtn.Enable()
									applyBtn.SetText(origText)
									dialog.ShowError(fmt.Errorf("go get succeeded but go mod tidy failed:\n%w\n\n%s", tidyErr, tidyOut), parent)
									return
								}
								// Success — flip the button to its done
								// state so it's visually obvious which
								// rows have been actioned when the user
								// is working through several alerts.
								// Stays disabled (idempotent anyway, but
								// the visual signal is the point).
								applyBtn.SetText("✓ Applied")
								applyBtn.Importance = widget.SuccessImportance
								applyBtn.Refresh()
								applyBtn.Disable()
								msg := fmt.Sprintf("Upgraded %s to %s.\n\ngo.mod and go.sum updated in this repo — commit those changes via Repo ▾ → Commit… to publish the fix.", alert.PackageName, fixVersion)
								combined := strings.TrimSpace(getOut + "\n" + tidyOut)
								if combined != "" {
									msg += "\n\nOutput:\n" + combined
								}
								dialog.ShowInformation("Fix applied", msg, parent)
							})
						}()
					}, parent)
			}
			buttons.Add(applyBtn)
		}

		header := container.NewBorder(nil, nil, severityBadge, buttons, pkgLabel)
		card := container.NewVBox(header, summaryLabel, metaLabel, widget.NewSeparator())
		cards.Add(card)
	}

	scroll := container.NewVScroll(cards)
	scroll.SetMinSize(fyne.NewSize(760, 460))
	d := dialog.NewCustom(title, "Close", scroll, parent)
	d.Resize(fyne.NewSize(840, 540))
	d.Show()
}

// openURLInBrowser launches the user's default browser at url via the
// platform's OS-open shim. Mirrors the pattern in
// explorer_actions.go's reveal/openInEditor helpers; not extracted into
// a shared util because the divergence in arguments (Reveal vs URL vs
// editor invocation) makes the shared abstraction less readable than
// three small switch statements.
func openURLInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// ─────────────────────────────────────────────────────────────────────────
// Push (v0.8.0)
//
// Push the current branch to a chosen remote. The remote dropdown
// defaults to the branch's configured upstream remote when there is
// one, or the first remote otherwise. A "Also set as upstream" checkbox
// appears (and defaults ticked) only when no upstream exists yet, so
// the first push automatically wires up tracking without burying it in
// a separate "set upstream" command.
//
// Force-push is deliberately not exposed — too easy to footgun, and the
// CLI is right there when you genuinely need it. `--force-with-lease`
// (the safer variant) could land later if it proves missed.
// ─────────────────────────────────────────────────────────────────────────

// currentBranchName returns the short name of the branch HEAD points
// at, or empty if HEAD is unborn or detached. Used by Push to seed the
// dialog and by Pull (Stage 4) similarly.
func currentBranchName(repoRoot string) string {
	out, err := exec.Command("git", "-C", repoRoot, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// branchUpstream returns the configured upstream for branch, in the
// form "<remote>/<branch>", or empty if none is configured.
func branchUpstream(repoRoot, branch string) string {
	if branch == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", branch+"@{upstream}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// resolvedRemoteURL returns the URL git will actually use for fetch
// against the named remote, after any `url.<base>.insteadof` rewrites
// have been applied. `ls-remote --get-url` is the canonical way to see
// the resolved URL; the stored URL (via `remote get-url`) may differ
// when an insteadof rewrite is in play. Empty on failure.
func resolvedRemoteURL(repoRoot, remote string) string {
	out, err := exec.Command("git", "-C", repoRoot, "ls-remote", "--get-url", remote).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// listRemoteNames returns just the remote names, sorted. Convenience
// wrapper over listRemoteEntries for the Push/Pull dropdowns where we
// don't need the URLs in the row data.
func listRemoteNames(repo *git.Repository) []string {
	entries, err := listRemoteEntries(repo)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.name)
	}
	return out
}

// runPush invokes git push for the given remote/branch combo. When
// setUpstream is true, adds -u so the local branch picks up the remote
// branch as its tracking config in one shot.
func runPush(repoRoot, remote, branch string, setUpstream bool) (output string, err error) {
	args := []string{"-C", repoRoot, "push"}
	if setUpstream {
		args = append(args, "-u")
	}
	args = append(args, remote, branch)
	cmd := exec.Command("git", args...)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%w\n%s", e, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// runForcePush invokes git push --force-with-lease for the given
// remote/branch. Unlike a plain --force, --force-with-lease refuses
// the push if the remote ref has moved beyond what git has cached
// locally — protecting against accidentally overwriting work pushed by
// someone else (or by you, on another machine) since your last fetch.
// This is the right recovery for the most common force-push need:
// publishing an amended commit when the original was already pushed.
func runForcePush(repoRoot, remote, branch string) (output string, err error) {
	cmd := exec.Command("git", "-C", repoRoot, "push", "--force-with-lease", remote, branch)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%w\n%s", e, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// pushNeedsForce reports whether a failed push's output indicates a
// non-fast-forward rejection (i.e. the local branch is behind or has
// diverged from the remote). Detected via the canonical
// "(non-fast-forward)" substring git prints alongside the [rejected]
// marker.
func pushNeedsForce(output string) bool {
	return strings.Contains(output, "non-fast-forward")
}

// showPushDialog opens the Push composer. The dialog stays open during
// the operation so the user can read git's output (push prints progress
// to stderr which CombinedOutput captures) and decide whether to close
// or push again with different settings.
//
// onLocalScan, when non-nil, gets a button in the inline vuln-alert row
// that GitHub Dependabot pushes back through the push output. Wiring
// the dep-scan firing as a callback rather than calling
// runDepScanForRepo directly keeps git_management.go free of an
// app-level dependency on the scanner.
//
// onCommitNeeded, when non-nil, is invoked from the "you need to commit
// first" prompt that fires when HEAD is unborn — same one-click escape
// hatch the Init success dialog could later use. Without it the user
// has to dismiss and navigate to Repo ▾ → Commit… manually.
//
// onPullNeeded, when non-nil, gets a "Pull first" button in the
// rescue row that surfaces after a non-fast-forward push rejection.
// Pull-first is the safer recovery when the rejection is caused by
// someone else having pushed — vs. force-with-lease, which is the
// right move for the amend-already-pushed case.
func showPushDialog(parent fyne.Window, repo *git.Repository, repoRoot string, onPushed func(), onLocalScan func(), onCommitNeeded func(), onPullNeeded func()) {
	if repo == nil || repoRoot == "" {
		return
	}
	branch := currentBranchName(repoRoot)
	if branch == "" {
		dialog.ShowError(fmt.Errorf("can't determine current branch — HEAD may be detached. Switch to a branch via the CLI first."), parent)
		return
	}
	// HEAD's symref resolves to a branch name even when the branch ref
	// itself doesn't exist yet (the unborn-HEAD case immediately after
	// init). git push would then fail with the cryptic
	// "src refspec X does not match any" — catch it here with a
	// friendlier prompt and offer to open the Commit dialog directly.
	if !hasAnyCommits(repoRoot) {
		dialog.ShowConfirm("Nothing to push",
			fmt.Sprintf("Branch %q has no commits yet — push needs at least one commit to send.\n\nOpen the Commit dialog now?", branch),
			func(yes bool) {
				if yes && onCommitNeeded != nil {
					onCommitNeeded()
				}
			}, parent)
		return
	}
	remotes := listRemoteNames(repo)
	if len(remotes) == 0 {
		dialog.ShowError(fmt.Errorf("no remotes configured. Add one via Repo ▾ → Manage Remotes…"), parent)
		return
	}

	upstream := branchUpstream(repoRoot, branch)
	hasUpstream := upstream != ""

	// Seed the remote dropdown — branch's existing upstream remote
	// wins, else first remote in alphabetical order.
	defaultRemote := remotes[0]
	if hasUpstream {
		if i := strings.Index(upstream, "/"); i > 0 {
			defaultRemote = upstream[:i]
		}
	}

	branchLabel := widget.NewLabel(branch)
	branchLabel.TextStyle = fyne.TextStyle{Monospace: true}

	upstreamText := "No upstream configured — this will be the first push."
	if hasUpstream {
		upstreamText = upstream
	}
	upstreamLabel := widget.NewLabel(upstreamText)
	upstreamLabel.TextStyle = fyne.TextStyle{Italic: true}
	upstreamLabel.Wrapping = fyne.TextWrapWord

	resolvedLabel := widget.NewLabel("")
	resolvedLabel.TextStyle = fyne.TextStyle{Monospace: true}
	resolvedLabel.Wrapping = fyne.TextWrapWord
	refreshResolved := func(remote string) {
		url := resolvedRemoteURL(repoRoot, remote)
		if url == "" {
			resolvedLabel.SetText("(could not resolve URL)")
			return
		}
		resolvedLabel.SetText(url)
	}
	refreshResolved(defaultRemote)

	remoteSelect := widget.NewSelect(remotes, func(selected string) {
		refreshResolved(selected)
	})
	remoteSelect.SetSelected(defaultRemote)

	// "Also set as upstream" — only meaningful when no upstream is set.
	setUpstreamCheck := widget.NewCheck("Also set as upstream for this branch", nil)
	setUpstreamCheck.SetChecked(!hasUpstream)
	if hasUpstream {
		setUpstreamCheck.Hide()
	}

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	// Inline alert for GitHub remote-side messages (currently:
	// Dependabot vulnerability summaries). Hidden by default —
	// silent success is the right behaviour when there's nothing
	// actionable. Populated and shown in the push success path.
	vulnHeadline := widget.NewLabel("")
	vulnHeadline.Wrapping = fyne.TextWrapWord
	vulnHeadline.TextStyle = fyne.TextStyle{Bold: true}
	openAlertsBtn := widget.NewButtonWithIcon("Open Dependabot alerts", theme.ComputerIcon(), nil)
	fetchAlertsBtn := widget.NewButtonWithIcon("Fetch alert details", theme.DownloadIcon(), nil)
	rescanBtn := widget.NewButtonWithIcon("Re-run local dep-scan", theme.SearchIcon(), nil)
	if onLocalScan == nil {
		rescanBtn.Disable()
	}
	vulnButtons := container.NewHBox(openAlertsBtn, fetchAlertsBtn, rescanBtn)
	vulnRow := container.NewVBox(vulnHeadline, vulnButtons)
	vulnRow.Hide()

	// Inline rescue row for non-fast-forward push rejections. Two common
	// causes: (a) someone else pushed since your last fetch (correct fix:
	// pull first), (b) you amended a commit that was already pushed
	// (correct fix: force-with-lease). The row offers force-with-lease
	// directly; a Pull-first button can join here once Stage 4 lands.
	rescueHeadline := widget.NewLabel("")
	rescueHeadline.Wrapping = fyne.TextWrapWord
	rescueHeadline.TextStyle = fyne.TextStyle{Bold: true}
	// Pull-first is the safer rescue (covers the someone-else-pushed
	// case); it leads in the rescue row. Force-with-lease is the
	// amend-already-pushed rescue and sits second with DangerImportance
	// styling so it reads as the heavier action.
	pullFirstBtn := widget.NewButtonWithIcon("Pull first", theme.DownloadIcon(), nil)
	pullFirstBtn.Importance = widget.HighImportance
	if onPullNeeded == nil {
		pullFirstBtn.Disable()
	} else {
		pullFirstBtn.OnTapped = func() { onPullNeeded() }
	}
	forcePushBtn := widget.NewButtonWithIcon("Force push (--force-with-lease)", theme.WarningIcon(), nil)
	forcePushBtn.Importance = widget.DangerImportance
	rescueRow := container.NewVBox(rescueHeadline, container.NewHBox(pullFirstBtn, forcePushBtn))
	rescueRow.Hide()

	pushBtn := widget.NewButtonWithIcon("Push", theme.UploadIcon(), nil)
	pushBtn.Importance = widget.HighImportance

	// applyPushSuccess centralises the post-success work: render git's
	// output into the status area and parse it for the Dependabot alert
	// headline so the clickable Open-alerts button surfaces regardless
	// of whether this was a regular push or a --force-with-lease retry.
	// Previously this logic only ran on the regular-push path, so the
	// vuln alert silently went missing after a force-push success.
	applyPushSuccess := func(summary, out string) {
		if out != "" {
			summary += "\n\n" + out
		}
		statusLabel.SetText(summary)
		pushBtn.SetText("✓ Pushed")
		pushBtn.Importance = widget.SuccessImportance
		pushBtn.Refresh()
		if alert := parseVulnAlert(out); alert != nil {
			headline := fmt.Sprintf("⚠ GitHub Security: %d vulnerabilit%s flagged on this push.",
				alert.count, pluralY(alert.count))
			if alert.severity != "" {
				headline += " (" + alert.severity + ")"
			}
			vulnHeadline.SetText(headline)
			if alert.url != "" {
				openAlertsBtn.OnTapped = func() {
					if e := openURLInBrowser(alert.url); e != nil {
						dialog.ShowError(fmt.Errorf("open browser: %w", e), parent)
					}
				}
				openAlertsBtn.Enable()
			} else {
				openAlertsBtn.Disable()
			}
			// Fetch-details button enables only when the URL is a
			// canonical github.com/<owner>/<repo>/security/dependabot
			// page (so we know who to query via gh api). Click runs
			// asynchronously with a 10s timeout and surfaces results
			// in a separate dialog; gh-CLI absence/auth issues get
			// specific hints rather than gh's raw stderr.
			if owner, repo, ok := parseDependabotURL(alert.url); ok {
				fetchAlertsBtn.OnTapped = func() {
					fetchAlertsBtn.Disable()
					originalText := fetchAlertsBtn.Text
					fetchAlertsBtn.SetText("Fetching…")
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						// parseDependabotURL only matches github.com URLs.
						alerts, err := fetchDependabotAlerts(ctx, "github.com", owner, repo)
						fyne.Do(func() {
							fetchAlertsBtn.Enable()
							fetchAlertsBtn.SetText(originalText)
							if err != nil {
								dialog.ShowError(err, parent)
								return
							}
							showDependabotAlertsDialog(parent, owner, repo, repoRoot, alerts)
						})
					}()
				}
				fetchAlertsBtn.Enable()
			} else {
				fetchAlertsBtn.Disable()
			}
			if onLocalScan != nil {
				rescanBtn.OnTapped = func() { onLocalScan() }
			}
			vulnRow.Show()
		}
		if onPushed != nil {
			onPushed()
		}
	}

	pushBtn.OnTapped = func() {
		pushBtn.SetText("Push")
		pushBtn.Importance = widget.HighImportance
		pushBtn.Refresh()
		remote := remoteSelect.Selected
		if remote == "" {
			statusLabel.SetText("Pick a remote first.")
			return
		}
		setUpstream := setUpstreamCheck.Checked && !hasUpstream
		statusLabel.SetText(fmt.Sprintf("Pushing %s → %s …", branch, remote))
		vulnRow.Hide()
		rescueRow.Hide()
		pushBtn.Disable()
		remoteSelect.Disable()
		setUpstreamCheck.Disable()
		go func() {
			out, err := runPush(repoRoot, remote, branch, setUpstream)
			fyne.Do(func() {
				pushBtn.Enable()
				remoteSelect.Enable()
				setUpstreamCheck.Enable()
				if err != nil {
					statusLabel.SetText("✗ Push failed:\n" + out)
					// Detect the canonical non-fast-forward
					// rejection and surface the safe force-push as
					// a one-click rescue. The most common cause
					// here is publishing an amended commit, which
					// git's own "use git pull" hint would handle
					// incorrectly (a merge would re-create the
					// pre-amend commit).
					if pushNeedsForce(out) {
						rescueHeadline.SetText("⚠ Push rejected as non-fast-forward — your local branch has diverged from the remote. " +
							"This usually means either (a) someone else pushed since your last fetch (correct fix: pull first), or " +
							"(b) you amended or rebased a commit that was already pushed (correct fix: force-with-lease).")
						forcePushBtn.OnTapped = func() {
							dialog.ShowConfirm("Force push?",
								fmt.Sprintf("Force-push %s → %s with --force-with-lease.\n\n"+
									"This overwrites the remote branch with your local version. The --force-with-lease safety check refuses the push if the remote has moved beyond what git has cached locally (i.e. if someone else pushed since your last fetch) — but it will not protect against overwriting work you haven't yet seen locally.\n\nContinue?",
									branch, remote),
								func(yes bool) {
									if !yes {
										return
									}
									statusLabel.SetText(fmt.Sprintf("Force-pushing %s → %s …", branch, remote))
									rescueRow.Hide()
									pushBtn.Disable()
									remoteSelect.Disable()
									go func() {
										fout, ferr := runForcePush(repoRoot, remote, branch)
										fyne.Do(func() {
											pushBtn.Enable()
											remoteSelect.Enable()
											if ferr != nil {
												statusLabel.SetText("✗ Force push failed:\n" + fout)
												return
											}
											applyPushSuccess(fmt.Sprintf("✓ Force-pushed %s → %s (with --force-with-lease).", branch, remote), fout)
										})
									}()
								}, parent)
						}
						rescueRow.Show()
					}
					return
				}
				applyPushSuccess(fmt.Sprintf("✓ Pushed %s → %s.", branch, remote), out)
			})
		}()
	}

	form := widget.NewForm(
		widget.NewFormItem("Branch", branchLabel),
		widget.NewFormItem("Upstream", upstreamLabel),
		widget.NewFormItem("Remote", remoteSelect),
		widget.NewFormItem("Resolved URL", resolvedLabel),
	)
	content := container.NewVBox(
		form,
		setUpstreamCheck,
		container.NewHBox(pushBtn),
		statusLabel,
		vulnRow,
		rescueRow,
	)

	d := dialog.NewCustom("Push — "+filepathBase(repoRoot), "Close", content, parent)
	d.Resize(fyne.NewSize(680, 460))
	d.Show()
}

// filepathBase is a thin shim around filepath.Base so the Push/Pull
// dialog titles can use it without importing filepath at file scope
// just for that.
func filepathBase(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// pluralY returns "y" for count==1 and "ies" otherwise — used for the
// "1 vulnerability" / "3 vulnerabilities" toggle in the inline alert.
func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// ─────────────────────────────────────────────────────────────────────────
// Pull (v0.8.0)
//
// Fetch + integrate from the branch's configured upstream. Strategy
// (merge vs rebase) defaults to the user's `pull.rebase` config and is
// overridable per-pull via a checkbox, matching the agreed scope. Merge
// or rebase conflicts surface verbatim — there's no in-app resolver,
// the user drops to their editor / CLI for that.
// ─────────────────────────────────────────────────────────────────────────

// pullRebaseDefault reads the user's `pull.rebase` config (local
// winning over global). Empty / unset / explicit "false" → false
// (git's historical default of merge); "true" → true. Used to seed the
// Pull dialog's "Rebase instead of merge" checkbox so the dialog
// honours the user's existing preference without overriding it.
func pullRebaseDefault(repoRoot string) bool {
	out, err := exec.Command("git", "-C", repoRoot, "config", "--get", "pull.rebase").Output()
	if err != nil {
		return false
	}
	v := strings.TrimSpace(strings.ToLower(string(out)))
	return v == "true" || v == "yes" || v == "1"
}

// runPull invokes git pull with an explicit --rebase or --no-rebase
// flag so the per-pull checkbox choice wins over the user's config
// without permanently overriding it. CombinedOutput captures both
// stdout and stderr so progress text and error messages both surface
// in the status area.
func runPull(repoRoot string, useRebase bool) (output string, err error) {
	args := []string{"-C", repoRoot, "pull"}
	if useRebase {
		args = append(args, "--rebase")
	} else {
		args = append(args, "--no-rebase")
	}
	cmd := exec.Command("git", args...)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%w\n%s", e, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// showPullDialog opens the Pull composer for the current branch.
// Requires a configured upstream — without one, git pull has nothing
// to pull from, so we refuse early with a friendly hint pointing at
// Push (which auto-sets-upstream on first run). Dialog stays open
// after the operation so the user can read the merge/rebase summary
// verbatim, including any conflict messages.
func showPullDialog(parent fyne.Window, repo *git.Repository, repoRoot string, onPulled func()) {
	if repo == nil || repoRoot == "" {
		return
	}
	branch := currentBranchName(repoRoot)
	if branch == "" {
		dialog.ShowError(fmt.Errorf("can't determine current branch — HEAD may be detached. Switch to a branch via the CLI first."), parent)
		return
	}
	upstream := branchUpstream(repoRoot, branch)
	if upstream == "" {
		dialog.ShowInformation("No upstream",
			fmt.Sprintf("Branch %q has no upstream configured — there's nothing to pull from.\n\nPush first via Repo ▾ → Push… (the first push will offer to set the upstream automatically).", branch),
			parent)
		return
	}

	// Split upstream "<remote>/<branch>" so we can show the resolved
	// URL — same useful "what git is about to actually use" reveal
	// that the Push dialog has, including any url.<base>.insteadof
	// rewrites the user has configured globally.
	remoteName := upstream
	if i := strings.Index(upstream, "/"); i > 0 {
		remoteName = upstream[:i]
	}

	branchLabel := widget.NewLabel(branch)
	branchLabel.TextStyle = fyne.TextStyle{Monospace: true}

	upstreamLabel := widget.NewLabel(upstream)
	upstreamLabel.TextStyle = fyne.TextStyle{Italic: true}

	resolvedLabel := widget.NewLabel(resolvedRemoteURL(repoRoot, remoteName))
	resolvedLabel.TextStyle = fyne.TextStyle{Monospace: true}
	resolvedLabel.Wrapping = fyne.TextWrapWord

	rebaseCheck := widget.NewCheck("Rebase instead of merge (--rebase)", nil)
	rebaseCheck.SetChecked(pullRebaseDefault(repoRoot))

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	pullBtn := widget.NewButtonWithIcon("Pull", theme.DownloadIcon(), nil)
	pullBtn.Importance = widget.HighImportance
	pullBtn.OnTapped = func() {
		useRebase := rebaseCheck.Checked
		mode := "merge"
		if useRebase {
			mode = "rebase"
		}
		statusLabel.SetText(fmt.Sprintf("Pulling %s (%s) …", upstream, mode))
		pullBtn.Disable()
		rebaseCheck.Disable()
		go func() {
			out, err := runPull(repoRoot, useRebase)
			fyne.Do(func() {
				pullBtn.Enable()
				rebaseCheck.Enable()
				if err != nil {
					statusLabel.SetText("✗ Pull failed:\n" + out)
					return
				}
				summary := fmt.Sprintf("✓ Pulled %s (%s).", upstream, mode)
				if out != "" {
					summary += "\n\n" + out
				}
				statusLabel.SetText(summary)
				if onPulled != nil {
					onPulled()
				}
			})
		}()
	}

	form := widget.NewForm(
		widget.NewFormItem("Branch", branchLabel),
		widget.NewFormItem("Upstream", upstreamLabel),
		widget.NewFormItem("Resolved URL", resolvedLabel),
	)
	content := container.NewVBox(
		form,
		rebaseCheck,
		container.NewHBox(pullBtn),
		statusLabel,
	)

	d := dialog.NewCustom("Pull — "+filepathBase(repoRoot), "Close", content, parent)
	d.Resize(fyne.NewSize(680, 420))
	d.Show()
}
