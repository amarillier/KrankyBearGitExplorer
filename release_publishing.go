package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/go-git/go-git/v5"
)

// release_publishing.go — v0.9.0 GitHub-release composer + uploader.
//
// Replaces the manual "go to github → draft a release → drag binaries
// → click Release" flow with a single dialog that auto-fills tag +
// title + notes (parsed from ReleaseNotes.txt), auto-discovers assets
// in bin/ + installers/, and uploads via the gh CLI. Works against
// github.com and GitHub Enterprise Server (GHES) instances — for GHES,
// the user must have run `gh auth login --hostname <ghes-host>` once
// so the gh CLI knows the host is github-compatible. When a repo has
// both a github.com remote and a GHES remote, github.com wins.

// releaseAssetDirs are the conventional subdirectories the dialog
// scans for upload candidates. Order matters for display (binaries
// before installers feels more natural to scan top-to-bottom).
var releaseAssetDirs = []string{"bin", "installers"}

// releaseNotesCandidateFiles is the ordered list of release-notes
// file names checked when opening the Release dialog. ReleaseNotes.txt
// is what this project uses; the others are common conventions for
// projects Allan may release with the same dialog later. The matching
// is filename-based; format-specific extraction beyond ReleaseNotes.txt
// is a future enhancement.
var releaseNotesCandidateFiles = []string{
	"ReleaseNotes.txt",
	"RELEASE_NOTES.txt",
	"RELEASE_NOTES.md",
	"RELEASES.md",
	"CHANGELOG.md",
}

// releaseAsset is one candidate file for upload to a GitHub release.
type releaseAsset struct {
	AbsPath       string // full path on disk, passed to gh release create
	DisplayPath   string // repo-relative path for the dialog (e.g. "bin/foo")
	Size          int64
	VersionInName string // semver substring found in the filename, "" if none
}

// filenameVersionRe matches a version-shaped substring inside a
// filename, e.g. "0.8.0" in "KrankyBearGitExplorerSetup_0.8.0.exe" or
// "0.8.0-1" in "krankybear-gitexplorer_0.8.0-1_amd64.deb". The match
// is conservative — three dotted numbers with an optional `-N` tail —
// so unrelated digit runs (e.g. file sizes in names) aren't picked up.
var filenameVersionRe = regexp.MustCompile(`\b(\d+\.\d+\.\d+(?:-\d+)?)\b`)

// discoverReleaseAssets scans the configured asset dirs under
// repoRoot, skipping hidden files (.DS_Store, .gitkeep, etc.) and
// returning a sorted, stable list. Missing directories are not
// errors — they just contribute zero candidates.
func discoverReleaseAssets(repoRoot string) ([]releaseAsset, error) {
	var assets []releaseAsset
	for _, dir := range releaseAssetDirs {
		fullDir := filepath.Join(repoRoot, dir)
		entries, err := os.ReadDir(fullDir)
		if err != nil {
			// Missing dir is fine; surface only unexpected errors.
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", fullDir, err)
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			abs := filepath.Join(fullDir, e.Name())
			a := releaseAsset{
				AbsPath:     abs,
				DisplayPath: dir + "/" + e.Name(),
				Size:        info.Size(),
			}
			if m := filenameVersionRe.FindStringSubmatch(e.Name()); m != nil {
				a.VersionInName = m[1]
			}
			assets = append(assets, a)
		}
	}
	sort.SliceStable(assets, func(i, j int) bool {
		return assets[i].DisplayPath < assets[j].DisplayPath
	})
	return assets, nil
}

// detectReleaseVersion finds the target repo's intended release version
// so the Release dialog defaults reflect the repo being released, not
// the explorer tool itself. FyneApp.toml is the source of truth for
// Allan's Fyne projects (its Version field is what gets baked into the
// binary). For non-Fyne repos we fall back to the newest semver-shaped
// git tag — slightly off (that's the previous release, not the one
// being prepared), but better than no signal. Returns "" when neither
// source yields anything, in which case the dialog opens with empty
// Tag/Title for the user to fill in by hand.
func detectReleaseVersion(repoRoot string) string {
	if b, err := os.ReadFile(filepath.Join(repoRoot, "FyneApp.toml")); err == nil {
		re := regexp.MustCompile(`(?m)^\s*Version\s*=\s*["']([^"']+)["']`)
		if m := re.FindSubmatch(b); m != nil {
			return string(m[1])
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "tag", "--list", "--sort=-v:refname").Output()
	if err != nil {
		return ""
	}
	tagRe := regexp.MustCompile(`^v?(\d+\.\d+\.\d+(?:[-+.][\w.-]+)?)$`)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if m := tagRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// extractReleaseNotes reads ReleaseNotes.txt (or whichever candidate
// file exists) at repoRoot and returns the section matching the given
// version. Format-specific to the "Version X.Y.Z - DATE" + ━━━ rule
// layout this project uses. Returns the body with the header line and
// trailing ━━━ separator removed, trimmed of leading/trailing blanks.
// When no candidate file exists, returns ("", nil) so the dialog
// opens with an empty notes field rather than refusing.
func extractReleaseNotes(repoRoot, version string) (notesPath, body string, err error) {
	var data []byte
	for _, candidate := range releaseNotesCandidateFiles {
		p := filepath.Join(repoRoot, candidate)
		if b, e := os.ReadFile(p); e == nil {
			data = b
			notesPath = p
			break
		}
	}
	if notesPath == "" {
		return "", "", nil
	}
	headerRe := regexp.MustCompile(`(?i)^version\s+` + regexp.QuoteMeta(version) + `\b`)
	nextSecRe := regexp.MustCompile(`(?i)^version\s+\d`)
	lines := strings.Split(string(data), "\n")
	var collecting bool
	var collected []string
	for _, line := range lines {
		if collecting {
			if nextSecRe.MatchString(line) {
				break
			}
			collected = append(collected, line)
			continue
		}
		if headerRe.MatchString(line) {
			collecting = true
		}
	}
	// Drop the ━━━ rule that follows the header line in this project's
	// notes format. The rule is a horizontal box-drawing character;
	// tolerant of leading/trailing whitespace.
	for len(collected) > 0 && strings.HasPrefix(strings.TrimSpace(collected[0]), "━") {
		collected = collected[1:]
	}
	body = strings.TrimSpace(strings.Join(collected, "\n"))
	if body == "" {
		return notesPath, "", fmt.Errorf("no section for version %q found in %s", version, notesPath)
	}
	return notesPath, body, nil
}

// ghHostAuthenticated reports whether gh has a token stored for host.
// Used to decide which non-github.com remotes are GHES candidates —
// gh's own auth state is the source of truth (we don't try to sniff
// GHES vs GitLab vs Gitea ourselves). A short timeout protects against
// a hung keyring or VPN-required host that's currently unreachable.
func ghHostAuthenticated(host string) bool {
	if host == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "gh", "auth", "status", "--hostname", host).Run() == nil
}

// findGitHubReleaseTarget iterates the repo's configured remotes and
// returns the host + owner + repo for the best github-compatible
// release target. Preference order:
//  1. github.com (any matching remote wins immediately)
//  2. any other host where `gh auth status --hostname <h>` succeeds,
//     treated as a GitHub Enterprise Server instance
//
// The explicit host return lets the caller pass `--repo HOST/OWNER/REPO`
// to every gh command, which is how gh routes API calls to a specific
// GHES instance instead of github.com.
func findGitHubReleaseTarget(repo *git.Repository) (host, owner, repoName string, ok bool) {
	remotes, err := repo.Remotes()
	if err != nil {
		return "", "", "", false
	}
	type candidate struct{ host, owner, repo string }
	var ghesCandidates []candidate
	for _, r := range remotes {
		cfg := r.Config()
		if cfg == nil {
			continue
		}
		for _, u := range cfg.URLs {
			h, path, _, _, parsed := parseGitURL(u)
			if !parsed {
				continue
			}
			parts := strings.SplitN(path, "/", 2)
			if len(parts) != 2 {
				continue
			}
			o := parts[0]
			n := strings.TrimSuffix(parts[1], ".git")
			if strings.EqualFold(h, "github.com") {
				return "github.com", o, n, true
			}
			ghesCandidates = append(ghesCandidates, candidate{h, o, n})
		}
	}
	for _, c := range ghesCandidates {
		if ghHostAuthenticated(c.host) {
			return c.host, c.owner, c.repo, true
		}
	}
	return "", "", "", false
}

// repoArg builds the HOST/OWNER/REPO value passed to gh's --repo flag.
// Always host-prefixed (even for github.com) so a single code path
// handles both github.com and GHES; gh accepts either form.
func repoArg(host, owner, repoName string) string {
	return host + "/" + owner + "/" + repoName
}

// authLoginHint returns the exact `gh auth login` command the user
// should run to authenticate against host. Surfaced in auth-failure
// errors so the fix is copy-pasteable.
func authLoginHint(host string) string {
	if host == "" || strings.EqualFold(host, "github.com") {
		return "gh auth login"
	}
	return "gh auth login --hostname " + host
}

// releaseTagExists checks via `gh release view` whether a release with
// the given tag already exists. Exit 0 → exists; anything else
// (including the "release not found" 404) → false. Network / auth
// failures bubble up as err so the caller can distinguish "couldn't
// check" from "doesn't exist".
func releaseTagExists(ctx context.Context, host, owner, repoName, tag string) (bool, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return false, fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/ to enable release publishing")
	}
	cmd := exec.CommandContext(ctx, "gh", "release", "view", tag, "--repo", repoArg(host, owner, repoName))
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	// gh prints "release not found" to stderr on miss; treat as
	// success-with-false. Anything else (auth failure, network) is
	// reported so the caller doesn't silently proceed.
	stderr := strings.TrimSpace(string(out))
	if strings.Contains(strings.ToLower(stderr), "not found") || strings.Contains(stderr, "HTTP 404") {
		return false, nil
	}
	if strings.Contains(stderr, "Bad credentials") || strings.Contains(stderr, "authentication") || strings.Contains(stderr, "401") {
		return false, fmt.Errorf("gh CLI is not authenticated for %s — run `%s` in a terminal, then retry", host, authLoginHint(host))
	}
	return false, fmt.Errorf("gh release view: %w\n%s", err, stderr)
}

// deleteReleaseTag removes an existing release via gh, used by the
// "overwrite existing release" flow. The associated git tag is also
// removed via --cleanup-tag so the subsequent create can re-establish
// it on the current HEAD.
func deleteReleaseTag(ctx context.Context, host, owner, repoName, tag string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "release", "delete", tag, "--repo", repoArg(host, owner, repoName), "--yes", "--cleanup-tag")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("gh release delete: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// runReleaseCreate invokes `gh release create` with the supplied
// fields and assets. The notes go through a temp file so the dialog's
// multi-line content (with newlines, special chars) round-trips
// cleanly — gh's --notes flag would force shell escaping that's
// fragile for prose. Tempfile is removed in a deferred Remove.
func runReleaseCreate(ctx context.Context, host, owner, repoName, tag, title, notes string, assets []string, prerelease, draft bool) (output string, releaseURL string, err error) {
	notesFile, err := os.CreateTemp("", "kbgitexplorer-release-notes-*.md")
	if err != nil {
		return "", "", fmt.Errorf("create temp notes file: %w", err)
	}
	defer os.Remove(notesFile.Name())
	if _, err := notesFile.WriteString(notes); err != nil {
		notesFile.Close()
		return "", "", fmt.Errorf("write temp notes file: %w", err)
	}
	notesFile.Close()

	args := []string{"release", "create", tag,
		"--repo", repoArg(host, owner, repoName),
		"--title", title,
		"--notes-file", notesFile.Name(),
	}
	if prerelease {
		args = append(args, "--prerelease")
	}
	if draft {
		args = append(args, "--draft")
	}
	args = append(args, assets...)
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, e := cmd.CombinedOutput()
	output = strings.TrimSpace(string(out))
	if e != nil {
		return output, "", fmt.Errorf("gh release create: %w\n%s", e, output)
	}
	// gh prints the release URL on its own line in the success
	// output (e.g. "https://<host>/owner/repo/releases/tag/v0.9.0").
	// Pluck it out so the success dialog can offer an Open button.
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") && strings.Contains(line, "/releases/") {
			releaseURL = line
			break
		}
	}
	// Fallback: synthesise the canonical URL from the tag using the
	// actual host so GHES instances get the right link.
	if releaseURL == "" {
		releaseURL = fmt.Sprintf("https://%s/%s/%s/releases/tag/%s", host, owner, repoName, tag)
	}
	return output, releaseURL, nil
}

// shortHeadSummary returns a short SHA + subject for HEAD, e.g.
// "abc1234 — v0.9.0". Used as the read-only Target display in the
// Release dialog so the user confirms they're tagging the right
// commit. Empty when HEAD is unborn or anything else goes wrong.
func shortHeadSummary(repoRoot string) string {
	out, err := exec.Command("git", "-C", repoRoot, "log", "-1", "--format=%h — %s").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// showReleaseDialog is the entry point for Repo ▾ → Release… / File
// menu → Release…. Composes a publish-to-GitHub release operation
// from the target repo's detected version (FyneApp.toml or latest
// semver tag), its ReleaseNotes.txt, and the contents of bin/ +
// installers/. The version is read from the *target* repo (the one
// open in the explorer), not from the explorer's own appVersion —
// otherwise releasing TaniumQuest would pre-fill with the explorer's
// version, which is never what you want.
func showReleaseDialog(parent fyne.Window, repo *git.Repository, repoRoot string) {
	if repo == nil || repoRoot == "" {
		return
	}
	host, owner, repoName, ok := findGitHubReleaseTarget(repo)
	if !ok {
		dialog.ShowError(fmt.Errorf("no github-compatible remote found for this repo — Release publishing needs either a github.com remote, or a GitHub Enterprise Server remote that gh is authenticated against (run `gh auth login --hostname <your-ghes-host>` first). Add or check remotes via Repo ▾ → Manage Remotes…."), parent)
		return
	}
	if _, err := exec.LookPath("gh"); err != nil {
		dialog.ShowError(fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/ to enable release publishing"), parent)
		return
	}

	repoVer := detectReleaseVersion(repoRoot)
	var defaultTag, defaultTitle string
	var notesPath, defaultNotes string
	var notesErr error
	if repoVer != "" {
		defaultTag = "v" + repoVer
		defaultTitle = fmt.Sprintf("v%s - %s", repoVer, time.Now().Format("January 2, 2006"))
		notesPath, defaultNotes, notesErr = extractReleaseNotes(repoRoot, repoVer)
	}

	assets, err := discoverReleaseAssets(repoRoot)
	if err != nil {
		dialog.ShowError(fmt.Errorf("discover assets: %w", err), parent)
		return
	}

	// ────────────── widgets ──────────────
	tagEntry := widget.NewEntry()
	tagEntry.SetText(defaultTag)

	titleEntry := widget.NewEntry()
	titleEntry.SetText(defaultTitle)

	targetText := shortHeadSummary(repoRoot)
	if targetText == "" {
		targetText = "(HEAD has no commits — release would create an empty tag)"
	}
	targetLabel := widget.NewLabel("HEAD: " + targetText)
	targetLabel.TextStyle = fyne.TextStyle{Italic: true}
	targetLabel.Wrapping = fyne.TextWrapWord

	// Show the host prefix when it isn't github.com so it's visually
	// obvious which instance the release will land on — important when
	// the user has both a github.com remote and a GHES remote configured.
	repoDisplay := owner + "/" + repoName
	if !strings.EqualFold(host, "github.com") {
		repoDisplay = host + "/" + repoDisplay
	}
	repoLabel := widget.NewLabel(repoDisplay)
	repoLabel.TextStyle = fyne.TextStyle{Monospace: true}

	notesEntry := widget.NewMultiLineEntry()
	notesEntry.SetText(defaultNotes)
	notesEntry.SetMinRowsVisible(10)
	notesEntry.SetPlaceHolder("Release notes (markdown rendered by GitHub)")
	notesHint := widget.NewLabel("")
	notesHint.Wrapping = fyne.TextWrapWord
	notesHint.TextStyle = fyne.TextStyle{Italic: true}
	switch {
	case repoVer == "":
		notesHint.SetText("Couldn't detect a version for this repo (no FyneApp.toml, no semver tags) — fill in Tag/Title and paste release notes above.")
	case notesPath == "":
		notesHint.SetText("No ReleaseNotes.txt / CHANGELOG.md found in repo root — paste your release notes above.")
	case notesErr != nil:
		notesHint.SetText(fmt.Sprintf("Couldn't extract notes for v%s from %s — paste your notes above. (%v)", repoVer, filepath.Base(notesPath), notesErr))
	default:
		notesHint.SetText(fmt.Sprintf("Prefilled from %s — edit before publishing if needed.", filepath.Base(notesPath)))
	}

	// Assets list. Each row is a checkbox + display path + size, plus
	// an inline ⚠ warning when the filename embeds a version that
	// doesn't match the target repo's detected version (the typical
	// sign you forgot to recompile after a version bump). Suppressed
	// when no version was detected, since we'd have nothing to compare
	// against and would spam a warning on every asset.
	type assetRow struct {
		check *widget.Check
		asset releaseAsset
	}
	var assetRows []assetRow
	assetsCol := container.NewVBox()
	for _, a := range assets {
		asset := a
		check := widget.NewCheck("", nil)
		check.SetChecked(true)
		nameLabel := widget.NewLabel(asset.DisplayPath)
		nameLabel.TextStyle = fyne.TextStyle{Monospace: true}
		sizeLabel := widget.NewLabel(humanSize(asset.Size))
		sizeLabel.TextStyle = fyne.TextStyle{Italic: true}
		warnLabel := widget.NewLabel("")
		warnLabel.TextStyle = fyne.TextStyle{Italic: true}
		if asset.VersionInName != "" && repoVer != "" && asset.VersionInName != repoVer {
			warnLabel.SetText(fmt.Sprintf("⚠ filename version %s ≠ %s", asset.VersionInName, repoVer))
		}
		row := container.NewBorder(nil, nil, check, sizeLabel, container.NewHBox(nameLabel, warnLabel))
		assetsCol.Add(row)
		assetRows = append(assetRows, assetRow{check: check, asset: asset})
	}
	if len(assetRows) == 0 {
		assetsCol.Add(widget.NewLabel("(no files found in bin/ or installers/ — did you compile yet?)"))
	}

	prereleaseCheck := widget.NewCheck("Mark as pre-release", nil)
	draftCheck := widget.NewCheck("Create as draft (review on GitHub before publishing)", nil)

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	openReleaseBtn := widget.NewButtonWithIcon("Open release on GitHub", theme.ComputerIcon(), nil)
	openReleaseBtn.Hide()

	publishBtn := widget.NewButtonWithIcon("Publish", theme.UploadIcon(), nil)
	publishBtn.Importance = widget.HighImportance

	publishBtn.OnTapped = func() {
		tag := strings.TrimSpace(tagEntry.Text)
		title := strings.TrimSpace(titleEntry.Text)
		notes := strings.TrimSpace(notesEntry.Text)
		if tag == "" || title == "" {
			dialog.ShowError(fmt.Errorf("tag and title are required"), parent)
			return
		}
		if notes == "" {
			dialog.ShowError(fmt.Errorf("release notes are required — paste or write them in the notes field"), parent)
			return
		}
		var selected []string
		for _, r := range assetRows {
			if r.check.Checked {
				selected = append(selected, r.asset.AbsPath)
			}
		}

		// Pre-flight: does this tag already have a release?
		statusLabel.SetText("Checking for existing release…")
		publishBtn.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			exists, checkErr := releaseTagExists(ctx, host, owner, repoName, tag)
			fyne.Do(func() {
				if checkErr != nil {
					publishBtn.Enable()
					statusLabel.SetText("")
					dialog.ShowError(checkErr, parent)
					return
				}
				proceed := func() {
					statusLabel.SetText(fmt.Sprintf("Publishing %s with %d asset(s)…", tag, len(selected)))
					go func() {
						ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Minute)
						defer cancel2()
						out, releaseURL, runErr := runReleaseCreate(ctx2, host, owner, repoName, tag, title, notes, selected, prereleaseCheck.Checked, draftCheck.Checked)
						fyne.Do(func() {
							if runErr != nil {
								// Re-enable for retry on failure — the
								// publish didn't land, so let the user
								// try again after fixing the cause.
								publishBtn.Enable()
								statusLabel.SetText("✗ Publish failed:\n" + out)
								return
							}
							// Flip the Publish button into a clearly
							// "done" state — same green-SuccessImportance
							// pattern as the Dependabot Apply-fix button
							// — so it's obvious at a glance that this
							// dialog's work is complete. Stays disabled;
							// re-publishing requires reopening the
							// dialog anyway (the overwrite-confirm
							// flow handles that path).
							publishBtn.SetText("✓ Published")
							publishBtn.Importance = widget.SuccessImportance
							publishBtn.Refresh()
							publishBtn.Disable()
							summary := fmt.Sprintf("✓ Published %s to %s.", tag, repoArg(host, owner, repoName))
							if out != "" {
								summary += "\n\n" + out
							}
							statusLabel.SetText(summary)
							openReleaseBtn.OnTapped = func() {
								if e := openURLInBrowser(releaseURL); e != nil {
									dialog.ShowError(fmt.Errorf("open browser: %w", e), parent)
								}
							}
							openReleaseBtn.Show()
						})
					}()
				}
				if !exists {
					proceed()
					return
				}
				// Tag already has a release — confirm overwrite. Overwrite
				// deletes the existing release first (with --cleanup-tag
				// so the git tag is removed too) then creates fresh, since
				// gh release create won't replace an existing release.
				dialog.ShowConfirm("Release already exists",
					fmt.Sprintf("A release with tag %q already exists at %s/%s/%s.\n\nOverwrite it? This deletes the existing release (and its tag) first, then re-creates with your new content.\n\nThe download counts and any external links to the old release will be lost.",
						tag, host, owner, repoName),
					func(confirmed bool) {
						if !confirmed {
							publishBtn.Enable()
							statusLabel.SetText("Cancelled — existing release left in place.")
							return
						}
						statusLabel.SetText(fmt.Sprintf("Deleting existing release %s…", tag))
						go func() {
							ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
							defer cancel2()
							delOut, delErr := deleteReleaseTag(ctx2, host, owner, repoName, tag)
							fyne.Do(func() {
								if delErr != nil {
									publishBtn.Enable()
									statusLabel.SetText("✗ Delete failed:\n" + delOut)
									return
								}
								proceed()
							})
						}()
					}, parent)
			})
		}()
	}

	form := widget.NewForm(
		widget.NewFormItem("Repo", repoLabel),
		widget.NewFormItem("Tag", tagEntry),
		widget.NewFormItem("Title", titleEntry),
		widget.NewFormItem("Target", targetLabel),
	)
	notesSection := container.NewVBox(
		widget.NewLabelWithStyle("Release notes", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		notesEntry,
		notesHint,
	)
	assetsScroll := container.NewVScroll(assetsCol)
	assetsScroll.SetMinSize(fyne.NewSize(600, 160))
	assetsSection := container.NewVBox(
		widget.NewLabelWithStyle(fmt.Sprintf("Assets (%d found)", len(assetRows)), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		assetsScroll,
	)
	// Footer is pinned outside the scroll so Publish + Close + the
	// pre-release/draft toggles stay visible regardless of how far the
	// user has scrolled through the asset list. Previously these lived
	// at the bottom of the scroll, which meant on shorter dialog heights
	// you had to scroll past the assets to find the Publish button.
	scrollContent := container.NewVBox(
		form,
		widget.NewSeparator(),
		notesSection,
		widget.NewSeparator(),
		assetsSection,
	)
	scroll := container.NewVScroll(scrollContent)
	scroll.SetMinSize(fyne.NewSize(760, 420))

	var d *dialog.CustomDialog
	closeBtn := widget.NewButton("Close", func() {
		if d != nil {
			d.Hide()
		}
	})
	footerRow := container.NewHBox(
		prereleaseCheck,
		draftCheck,
		layout.NewSpacer(),
		openReleaseBtn,
		publishBtn,
		closeBtn,
	)
	footer := container.NewVBox(
		widget.NewSeparator(),
		statusLabel,
		footerRow,
	)
	root := container.NewBorder(nil, footer, nil, nil, scroll)

	dialogTitle := "Release"
	if repoVer != "" {
		dialogTitle = "Release — v" + repoVer
	}
	d = dialog.NewCustomWithoutButtons(dialogTitle, root, parent)
	d.Resize(fyne.NewSize(820, 720))
	d.Show()
}
