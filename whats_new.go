package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
	"github.com/go-git/go-git/v5/plumbing"
)

// prefLastSeenHeadPrefix namespaces the per-repo "last seen HEAD" markers
// inside Fyne preferences. Key format: lastSeenHead:<absRepoRoot> → SHA
// (40-char string). One key per repo keeps the lookup O(1) and avoids
// rewriting a single JSON blob every time HEAD advances; the trade-off is
// the preference file grows with the number of repos ever opened. Each
// entry is ~50 bytes, so the file stays small even after years of use.
const prefLastSeenHeadPrefix = "lastSeenHead:"

func lastSeenHeadKey(repoRoot string) string {
	return prefLastSeenHeadPrefix + repoRoot
}

// loadLastSeenHead returns the stored marker SHA for this repo, or the
// zero hash if there's no marker yet (first time we're seeing this repo).
func loadLastSeenHead(a fyne.App, repoRoot string) plumbing.Hash {
	if repoRoot == "" {
		return plumbing.ZeroHash
	}
	raw := strings.TrimSpace(a.Preferences().StringWithFallback(lastSeenHeadKey(repoRoot), ""))
	if raw == "" {
		return plumbing.ZeroHash
	}
	return plumbing.NewHash(raw)
}

// saveLastSeenHead updates the marker for this repo. Empty repoRoot or
// zero hash is a no-op so callers can pass v.repoRoot / current HEAD
// without guarding around the "not in a repo" case.
func saveLastSeenHead(a fyne.App, repoRoot string, head plumbing.Hash) {
	if repoRoot == "" || head.IsZero() {
		return
	}
	a.Preferences().SetString(lastSeenHeadKey(repoRoot), head.String())
}

// countCommitsAhead returns the number of commits reachable from HEAD but
// not from stored. Uses `git rev-list --count <stored>..HEAD` which
// handles all the edge cases for us:
//   - stored is HEAD's direct ancestor → returns the count
//   - stored == HEAD → returns 0
//   - stored isn't reachable from HEAD (force-push, hard reset) → range
//     is empty, returns 0
//   - stored doesn't exist in the object DB → command errors
//   - HEAD is BEHIND stored → also returns 0 (we treat backward motion
//     as "no new commits" since the user did the moving)
//
// Returns (0, error) when git itself reports a problem; the caller's
// usual policy is "silently advance the marker and skip the banner".
func countCommitsAhead(repoRoot string, stored, head plumbing.Hash) (int, error) {
	if repoRoot == "" || stored.IsZero() || head.IsZero() {
		return 0, nil
	}
	if stored == head {
		return 0, nil
	}
	cmd := exec.Command("git", "-C", repoRoot, "rev-list", "--count", fmt.Sprintf("%s..%s", stored.String(), head.String()))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("git rev-list --count: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	n, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		return 0, fmt.Errorf("parse rev-list count: %w", err)
	}
	return n, nil
}

// buildWhatsNewBanner builds the strip shown above the filter bar when
// new commits have landed since the user last visited this repo. The
// returned container is hidden by default; evaluateWhatsNew (in
// explorer_view.go) shows it and populates the label + base SHA when
// there's something to surface.
//
// Layout: <italic label>  [Compare] [Dismiss]
//
// Compare opens Repo History with the stored marker pre-set as the
// compare base — one click to see the diff against last visit.
// Dismiss hides the banner and advances the marker to current HEAD,
// so the same banner doesn't re-appear next time without a real change.
func (v *explorerView) buildWhatsNewBanner() *fyne.Container {
	v.whatsNewLabel = widget.NewLabel("")
	v.whatsNewLabel.TextStyle = fyne.TextStyle{Italic: true}
	v.whatsNewLabel.Truncation = fyne.TextTruncateEllipsis

	compareBtn := ttwidget.NewButton("Compare", func() { v.whatsNewCompare() })
	compareBtn.SetToolTip("Open Repo History with last visit's HEAD pre-set as the compare base — shows what changed since you last opened this repo.")
	compareBtn.Importance = widget.MediumImportance

	dismissBtn := ttwidget.NewButton("Dismiss", func() { v.whatsNewDismiss() })
	dismissBtn.SetToolTip("Hide this banner and mark current HEAD as 'seen' so it doesn't reappear unless new commits land.")
	dismissBtn.Importance = widget.LowImportance

	row := container.NewBorder(nil, nil, nil,
		container.NewHBox(compareBtn, dismissBtn),
		v.whatsNewLabel,
	)
	row.Hide()
	return row
}

// evaluateWhatsNew runs after a repo loads (in doLoadFolder) and decides
// whether to show the banner. Branches:
//
//   - No marker stored yet (first visit to this repo): silently capture
//     current HEAD as the marker, no banner.
//   - Marker == current HEAD: hide banner (nothing new since last visit).
//   - Marker is an ancestor of HEAD and ahead by N > 0: show banner.
//   - Marker exists but isn't reachable / can't be counted (force-push,
//     pruned commit, git error): silently re-capture current HEAD, no
//     banner. The user did something that broke the chain — re-anchor
//     and move on.
func (v *explorerView) evaluateWhatsNew() {
	if v.whatsNewRow == nil {
		return
	}
	v.whatsNewRow.Hide()
	v.whatsNewBaseSHA = plumbing.ZeroHash

	if v.repo == nil || v.repoRoot == "" {
		return
	}
	headRef, err := v.repo.Head()
	if err != nil {
		return // headless / detached weirdness — skip
	}
	headHash := headRef.Hash()

	stored := loadLastSeenHead(v.app, v.repoRoot)
	if stored.IsZero() {
		// First visit — capture and stay quiet.
		saveLastSeenHead(v.app, v.repoRoot, headHash)
		return
	}
	if stored == headHash {
		return // nothing new
	}
	count, err := countCommitsAhead(v.repoRoot, stored, headHash)
	if err != nil || count <= 0 {
		// Unreachable / pruned / behind — re-anchor and stay quiet.
		saveLastSeenHead(v.app, v.repoRoot, headHash)
		return
	}

	v.whatsNewBaseSHA = stored
	plural := "commits"
	if count == 1 {
		plural = "commit"
	}
	v.whatsNewLabel.SetText(fmt.Sprintf("%d new %s since you last opened this repo (since %s).", count, plural, shortSHA(stored.String())))
	v.whatsNewRow.Show()
}

// whatsNewCompare handles the banner's Compare button: opens Repo History
// with the stored marker pre-set as the compare base. Mirrors the manual
// "Pick as compare base" flow in history_view but skips the extra click.
func (v *explorerView) whatsNewCompare() {
	if v.repo == nil || v.repoRoot == "" || v.whatsNewBaseSHA.IsZero() {
		return
	}
	hv := openHistoryWindow(v.app, v.repo, v.repoRoot, v.win, "")
	if hv == nil {
		return
	}
	if baseCommit, err := v.repo.CommitObject(v.whatsNewBaseSHA); err == nil {
		hv.setCompareBase(baseCommit)
	}
	// The banner has done its job — advance the marker so it doesn't
	// re-appear next time unless something new actually lands.
	v.whatsNewDismiss()
}

// whatsNewDismiss is the Dismiss handler: hide the banner and advance the
// marker to current HEAD. Also called by whatsNewCompare since opening
// Repo History counts as acknowledging the new commits.
func (v *explorerView) whatsNewDismiss() {
	if v.whatsNewRow != nil {
		v.whatsNewRow.Hide()
	}
	if v.repo == nil || v.repoRoot == "" {
		return
	}
	if headRef, err := v.repo.Head(); err == nil {
		saveLastSeenHead(v.app, v.repoRoot, headRef.Hash())
	}
	v.whatsNewBaseSHA = plumbing.ZeroHash
}

// advanceLastSeenHeadForCurrentRepo updates the marker to current HEAD
// for the explorer's currently-loaded repo. Called from cross-repo
// switch points and window close/quit so closing the explorer counts as
// an implicit acknowledgement of whatever state was on screen.
func (v *explorerView) advanceLastSeenHeadForCurrentRepo() {
	if v.repo == nil || v.repoRoot == "" {
		return
	}
	if headRef, err := v.repo.Head(); err == nil {
		saveLastSeenHead(v.app, v.repoRoot, headRef.Hash())
	}
}
