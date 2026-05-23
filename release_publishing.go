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
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/go-git/go-git/v5"
)

// release_publishing.go — v0.9.0 GitHub-release composer + uploader.
//
// Replaces the manual "go to github.com → draft a release → drag
// binaries → click Release" flow with a single dialog that
// auto-fills tag + title + notes (parsed from ReleaseNotes.txt),
// auto-discovers assets in bin/ + installers/, and uploads via the
// gh CLI. Strictly host-scoped to github.com — the only release
// platform the gh CLI supports — so the dialog refuses to open for
// repos without a github.com remote.

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

// findGitHubOwnerRepo iterates the repo's configured remotes and
// returns the first github.com remote's owner + repo path. Used to
// scope gh release commands with an explicit --repo flag rather than
// relying on gh's own auto-detection (which can pick the wrong remote
// when multiple are configured).
func findGitHubOwnerRepo(repo *git.Repository) (owner, repoName string, ok bool) {
	remotes, err := repo.Remotes()
	if err != nil {
		return "", "", false
	}
	for _, r := range remotes {
		cfg := r.Config()
		if cfg == nil {
			continue
		}
		for _, u := range cfg.URLs {
			host, path, _, _, parsed := parseGitURL(u)
			if !parsed || !strings.EqualFold(host, "github.com") {
				continue
			}
			parts := strings.SplitN(path, "/", 2)
			if len(parts) != 2 {
				continue
			}
			return parts[0], strings.TrimSuffix(parts[1], ".git"), true
		}
	}
	return "", "", false
}

// releaseTagExists checks via `gh release view` whether a release with
// the given tag already exists. Exit 0 → exists; anything else
// (including the "release not found" 404) → false. Network / auth
// failures bubble up as err so the caller can distinguish "couldn't
// check" from "doesn't exist".
func releaseTagExists(ctx context.Context, owner, repoName, tag string) (bool, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return false, fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/ to enable release publishing")
	}
	cmd := exec.CommandContext(ctx, "gh", "release", "view", tag, "--repo", owner+"/"+repoName)
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
		return false, fmt.Errorf("gh CLI is not authenticated — run `gh auth login` in a terminal, then retry")
	}
	return false, fmt.Errorf("gh release view: %w\n%s", err, stderr)
}

// deleteReleaseTag removes an existing release via gh, used by the
// "overwrite existing release" flow. The associated git tag is also
// removed via --cleanup-tag so the subsequent create can re-establish
// it on the current HEAD.
func deleteReleaseTag(ctx context.Context, owner, repoName, tag string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "release", "delete", tag, "--repo", owner+"/"+repoName, "--yes", "--cleanup-tag")
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
func runReleaseCreate(ctx context.Context, owner, repoName, tag, title, notes string, assets []string, prerelease, draft bool) (output string, releaseURL string, err error) {
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
		"--repo", owner + "/" + repoName,
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
	// output (e.g. "https://github.com/owner/repo/releases/tag/v0.9.0").
	// Pluck it out so the success dialog can offer an Open button.
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") && strings.Contains(line, "/releases/") {
			releaseURL = line
			break
		}
	}
	// Fallback: synthesise the canonical URL from the tag.
	if releaseURL == "" {
		releaseURL = fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", owner, repoName, tag)
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
// from the current repo's appVersion, ReleaseNotes.txt, and the
// contents of bin/ + installers/.
func showReleaseDialog(parent fyne.Window, repo *git.Repository, repoRoot string) {
	if repo == nil || repoRoot == "" {
		return
	}
	owner, repoName, ok := findGitHubOwnerRepo(repo)
	if !ok {
		dialog.ShowError(fmt.Errorf("no github.com remote configured for this repo — Release publishing requires a github.com remote. Add one via Repo ▾ → Manage Remotes… first."), parent)
		return
	}
	if _, err := exec.LookPath("gh"); err != nil {
		dialog.ShowError(fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/ to enable release publishing"), parent)
		return
	}

	defaultTag := "v" + appVersion
	defaultTitle := fmt.Sprintf("v%s - %s", appVersion, time.Now().Format("January 2, 2006"))

	notesPath, defaultNotes, notesErr := extractReleaseNotes(repoRoot, appVersion)

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

	repoLabel := widget.NewLabel(owner + "/" + repoName)
	repoLabel.TextStyle = fyne.TextStyle{Monospace: true}

	notesEntry := widget.NewMultiLineEntry()
	notesEntry.SetText(defaultNotes)
	notesEntry.SetMinRowsVisible(10)
	notesEntry.SetPlaceHolder("Release notes (markdown rendered by GitHub)")
	notesHint := widget.NewLabel("")
	notesHint.Wrapping = fyne.TextWrapWord
	notesHint.TextStyle = fyne.TextStyle{Italic: true}
	switch {
	case notesPath == "":
		notesHint.SetText("No ReleaseNotes.txt / CHANGELOG.md found in repo root — paste your release notes above.")
	case notesErr != nil:
		notesHint.SetText(fmt.Sprintf("Couldn't extract notes for v%s from %s — paste your notes above. (%v)", appVersion, filepath.Base(notesPath), notesErr))
	default:
		notesHint.SetText(fmt.Sprintf("Prefilled from %s — edit before publishing if needed.", filepath.Base(notesPath)))
	}

	// Assets list. Each row is a checkbox + display path + size, plus
	// an inline ⚠ warning when the filename embeds a version that
	// doesn't match appVersion (the typical sign you forgot to
	// recompile after a version bump).
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
		if asset.VersionInName != "" && asset.VersionInName != appVersion {
			warnLabel.SetText(fmt.Sprintf("⚠ filename version %s ≠ %s", asset.VersionInName, appVersion))
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
			exists, checkErr := releaseTagExists(ctx, owner, repoName, tag)
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
						out, releaseURL, runErr := runReleaseCreate(ctx2, owner, repoName, tag, title, notes, selected, prereleaseCheck.Checked, draftCheck.Checked)
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
							summary := fmt.Sprintf("✓ Published %s to %s/%s.", tag, owner, repoName)
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
					fmt.Sprintf("A release with tag %q already exists at github.com/%s/%s.\n\nOverwrite it? This deletes the existing release (and its tag) first, then re-creates with your new content.\n\nThe download counts and any external links to the old release will be lost.",
						tag, owner, repoName),
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
							delOut, delErr := deleteReleaseTag(ctx2, owner, repoName, tag)
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
	content := container.NewVBox(
		form,
		widget.NewSeparator(),
		notesSection,
		widget.NewSeparator(),
		assetsSection,
		widget.NewSeparator(),
		prereleaseCheck,
		draftCheck,
		container.NewHBox(publishBtn, openReleaseBtn),
		statusLabel,
	)
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(760, 600))

	d := dialog.NewCustom("Release — v"+appVersion, "Close", scroll, parent)
	d.Resize(fyne.NewSize(820, 720))
	d.Show()
}
