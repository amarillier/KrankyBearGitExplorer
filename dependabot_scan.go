package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/go-git/go-git/v5"
)

// depTarget is one repository the Dependabot sweep will query. repoRoot is the
// on-disk path when the repo is also cloned locally (so the per-repo detail
// view can offer go-get fixes); it's "" for repos discovered only via owner
// enumeration on GitHub.
type depTarget struct {
	host     string // "" ⇒ github.com
	owner    string
	repo     string
	repoRoot string
}

func (t depTarget) hostOrDefault() string {
	if t.host == "" {
		return "github.com"
	}
	return t.host
}

func (t depTarget) key() string {
	return strings.ToLower(t.hostOrDefault() + "/" + t.owner + "/" + t.repo)
}

// depErrBucket classifies why a repo couldn't be scanned, so the aggregate can
// group skipped repos by reason (with advice) rather than dumping raw stderr.
type depErrBucket int

const (
	depErrOther depErrBucket = iota
	depErrNotAuthorized
	depErrNotFound
	depErrDisabled
	depErrAuth
	depErrThirdParty // pre-skipped: owner isn't yours, never called the API
)

// classifyDepError maps a gh-api failure to a bucket by inspecting gh's
// combined output (the JSON body carries "status":"403" etc. and gh prints a
// human "(HTTP 403)" line). Order matters: disabled/auth are checked before the
// generic 403/404 so their more specific wording wins.
func classifyDepError(err error) depErrBucket {
	if err == nil {
		return depErrOther
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "dependabot") && (strings.Contains(s, "disabled") || strings.Contains(s, "not enabled")):
		return depErrDisabled
	case strings.Contains(s, "bad credentials") || strings.Contains(s, "authentication") || strings.Contains(s, "401"):
		return depErrAuth
	case strings.Contains(s, "not authorized") || strings.Contains(s, "http 403") || strings.Contains(s, `"status":"403"`):
		return depErrNotAuthorized
	case strings.Contains(s, "not found") || strings.Contains(s, "http 404") || strings.Contains(s, `"status":"404"`):
		return depErrNotFound
	default:
		return depErrOther
	}
}

// ownerRepo is one entry from `gh repo list <owner>`.
type ownerRepo struct {
	owner string
	repo  string
}

// depAlertResult is the per-target outcome of the sweep.
type depAlertResult struct {
	target depTarget
	alerts []dependabotAlert
	err    error
}

// depAlertSummary feeds the header badge after a sweep.
type depAlertSummary struct {
	totalAlerts int
	when        time.Time
}

// ghConcurrency caps how many `gh api` calls run at once during a sweep — a
// gentle limit that keeps us well under GitHub's REST rate limit and avoids
// spawning dozens of gh processes simultaneously.
const ghConcurrency = 5

// depAlertCallTimeout bounds each per-repo `gh api` call.
const depAlertCallTimeout = 25 * time.Second

// ghOutput runs a gh subcommand and returns stdout, mapping the common
// missing-binary and not-authenticated cases to actionable errors (mirroring
// fetchDependabotAlerts' handling). stderr is captured separately so it
// doesn't pollute the JSON stdout the callers parse.
func ghOutput(ctx context.Context, args ...string) ([]byte, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/ and run `gh auth login`")
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		es := strings.TrimSpace(stderr.String())
		if strings.Contains(es, "Bad credentials") || strings.Contains(es, "authentication") || strings.Contains(es, "401") {
			return nil, fmt.Errorf("gh CLI is not authenticated — run `gh auth login` in a terminal, then retry")
		}
		return nil, fmt.Errorf("gh %s: %w\n%s", strings.Join(args, " "), err, es)
	}
	return []byte(stdout.String()), nil
}

// ghAuthenticatedUser returns the login of the github.com account gh is
// authenticated as, used to seed the owners-to-scan list.
func ghAuthenticatedUser(ctx context.Context) (string, error) {
	out, err := ghOutput(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// listOwnerRepos enumerates the repos under owner via `gh repo list`. Archived
// repos are excluded unless includeArchived is set; forks are kept (a fork can
// still carry its own Dependabot alerts).
func listOwnerRepos(ctx context.Context, owner string, includeArchived bool) ([]ownerRepo, error) {
	out, err := ghOutput(ctx, "repo", "list", owner,
		"--limit", "1000",
		"--json", "nameWithOwner,isArchived")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		NameWithOwner string `json:"nameWithOwner"`
		IsArchived    bool   `json:"isArchived"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse gh repo list: %w", err)
	}
	var repos []ownerRepo
	for _, r := range raw {
		if r.IsArchived && !includeArchived {
			continue
		}
		parts := strings.SplitN(r.NameWithOwner, "/", 2)
		if len(parts) != 2 {
			continue
		}
		repos = append(repos, ownerRepo{owner: parts[0], repo: parts[1]})
	}
	return repos, nil
}

// firstNonGitHubHost returns the host of the first remote that parses to a
// non-github.com host (i.e. a likely GHES instance), or "" if none. Used to
// produce a re-auth hint when findGitHubReleaseTarget rejects a repo only
// because gh isn't authenticated to its Enterprise host.
func firstNonGitHubHost(repo *git.Repository) string {
	if repo == nil {
		return ""
	}
	remotes, err := repo.Remotes()
	if err != nil {
		return ""
	}
	for _, r := range remotes {
		cfg := r.Config()
		if cfg == nil {
			continue
		}
		for _, u := range cfg.URLs {
			if h, _, _, _, ok := parseGitURL(u); ok && !strings.EqualFold(h, "github.com") {
				return h
			}
		}
	}
	return ""
}

// localGitHubTargets maps the locally-discovered repos (the same set the local
// dep-scan sweep uses) to depTargets. findGitHubReleaseTarget returns a GHES
// host only when gh is authenticated there, so internal clones are included
// automatically once that token is valid and silently absent otherwise.
// repoRoot is carried through so the detail view can offer fixes for cloned
// repos.
func localGitHubTargets(a fyne.App) []depTarget {
	var out []depTarget
	for _, root := range discoverRepos(loadDepScanRoots(a), depScanMaxDepth) {
		repo, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
		if err != nil {
			continue
		}
		host, owner, name, ok := findGitHubReleaseTarget(repo)
		if !ok {
			continue
		}
		out = append(out, depTarget{host: host, owner: owner, repo: name, repoRoot: root})
	}
	return out
}

// mergeDepTargets unions owner-enumerated repos with locally-discovered ones,
// de-duplicating by lowercased owner/repo. When a repo appears in both sets the
// local entry wins, so its repoRoot (enabling go-get fixes) is preserved.
// Result is sorted by key for stable display + testability. Pure function.
func mergeDepTargets(ownerRepos []ownerRepo, local []depTarget) []depTarget {
	byKey := make(map[string]depTarget)
	for _, o := range ownerRepos {
		t := depTarget{host: "github.com", owner: o.owner, repo: o.repo}
		byKey[t.key()] = t
	}
	// Local entries overwrite, carrying repoRoot.
	for _, t := range local {
		byKey[t.key()] = t
	}
	out := make([]depTarget, 0, len(byKey))
	for _, t := range byKey {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

// collectDepTargets builds the full sweep set: every configured owner's repos
// (github.com) unioned with the local github.com repos. Owner-enumeration
// errors are returned alongside whatever targets were gathered, so the sweep
// can still proceed on the local set if `gh repo list` fails for one owner.
func collectDepTargets(a fyne.App) ([]depTarget, []string) {
	includeArchived := a.Preferences().Bool(prefDepAlertIncludeArchived)
	var enumerated []ownerRepo
	var warnings []string

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, owner := range loadDepAlertOwners(a) {
		owner = strings.TrimSpace(owner)
		if owner == "" {
			continue
		}
		repos, err := listOwnerRepos(ctx, owner, includeArchived)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not enumerate %q: %v", owner, err))
			continue
		}
		enumerated = append(enumerated, repos...)
	}

	return mergeDepTargets(enumerated, localGitHubTargets(a)), warnings
}

// ownedGitHubOwners returns the lowercased set of github.com owners considered
// "yours" — the configured owners plus the authenticated gh login. Used to
// pre-skip locally-cloned third-party repos (whose Dependabot alerts you can't
// read anyway) rather than firing doomed 403 calls at them.
func ownedGitHubOwners(a fyne.App) map[string]bool {
	owned := make(map[string]bool)
	for _, o := range loadDepAlertOwners(a) {
		if o = strings.TrimSpace(o); o != "" {
			owned[strings.ToLower(o)] = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if login, err := ghAuthenticatedUser(ctx); err == nil && login != "" {
		owned[strings.ToLower(login)] = true
	}
	return owned
}

// partitionTargets splits the sweep set into repos worth querying and
// third-party github.com clones to skip. A github.com target whose owner isn't
// yours is bucketed as third-party (no API call). GHES targets are always
// attempted — ownership there can't be inferred from the owner alone and you
// likely have access to internal repos you've cloned.
func partitionTargets(a fyne.App, targets []depTarget) (scan, thirdParty []depTarget) {
	owned := ownedGitHubOwners(a)
	for _, t := range targets {
		if strings.EqualFold(t.hostOrDefault(), "github.com") && !owned[strings.ToLower(t.owner)] {
			thirdParty = append(thirdParty, t)
			continue
		}
		scan = append(scan, t)
	}
	return scan, thirdParty
}

// sweepDependabotAlerts runs the all-repos sweep headlessly (no dialogs) and
// returns the total open-alert count for the badge. ok is false when there's
// nothing to scan, or when every scanned repo errored (gh logged out / offline)
// — so the daily scheduler never paints a misleading "alerts clear" badge off a
// total failure. Reuses the same collect/partition/fetch path as the
// interactive sweep.
func sweepDependabotAlerts(a fyne.App) (total int, ok bool) {
	targets, _ := collectDepTargets(a)
	if len(targets) == 0 {
		return 0, false
	}
	scan, _ := partitionTargets(a, targets)
	if len(scan) == 0 {
		return 0, false
	}
	results := fetchAlertsForTargets(scan)
	scannedOK := 0
	for _, r := range results {
		if r.err == nil {
			scannedOK++
			total += len(r.alerts)
		}
	}
	if scannedOK == 0 {
		return 0, false
	}
	return total, true
}

// dueForDailyScan decides whether the daily sweep should fire now: enabled, a
// valid HH:MM time, not already run today, and now is at/after today's slot.
// The lastRun date guard makes it once-per-day; checking "at/after the slot"
// (rather than "exactly at") means a machine asleep through the slot runs the
// sweep at the next check after waking. Pure → unit-tested.
func dueForDailyScan(enabled bool, timeStr, lastRun string, now time.Time) bool {
	if !enabled {
		return false
	}
	sched, err := time.Parse("15:04", strings.TrimSpace(timeStr))
	if err != nil {
		return false
	}
	if lastRun == now.Format("2006-01-02") {
		return false
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), sched.Hour(), sched.Minute(), 0, 0, now.Location())
	return !now.Before(slot)
}

// runDependabotScanAll fetches open Dependabot alerts across every target
// concurrently, then shows the aggregate view and updates the badge. Per-repo
// failures (Dependabot disabled, 404, auth) are collected and surfaced rather
// than aborting the sweep — so a single bad repo never silently hides the rest.
func runDependabotScanAll(a fyne.App, parent fyne.Window, onResult func(depAlertSummary)) {
	targets, warnings := collectDepTargets(a)
	if len(targets) == 0 {
		msg := "No GitHub repositories to scan yet.\n\nAdd one or more owners/orgs to enumerate (and/or configure local source folders), then try again.\n\nOpen the Dependabot configuration now?"
		if len(warnings) > 0 {
			msg = strings.Join(warnings, "\n") + "\n\n" + msg
		}
		dialog.ShowConfirm("Dependabot — Scan All Repos", msg, func(yes bool) {
			if yes {
				showDepAlertConfig(a, parent)
			}
		}, parent)
		return
	}

	// Pre-skip third-party github.com clones we can't read — no point firing
	// doomed 403 calls; they're bucketed in the aggregate instead.
	scan, thirdParty := partitionTargets(a, targets)

	progBar := widget.NewProgressBarInfinite()
	progLbl := widget.NewLabel(fmt.Sprintf("Fetching Dependabot alerts for %d repositories via gh…\n\nThis queries GitHub's security API for each repo. (%d third-party clone(s) skipped.)", len(scan), len(thirdParty)))
	progLbl.Wrapping = fyne.TextWrapWord
	progDlg := dialog.NewCustom("Dependabot — Scan All Repos", "Hide (continues in background)", container.NewVBox(progLbl, progBar), parent)
	progDlg.Resize(fyne.NewSize(560, 200))
	progDlg.Show()

	go func() {
		results := fetchAlertsForTargets(scan)
		total := 0
		for _, r := range results {
			if r.err == nil {
				total += len(r.alerts)
			}
		}
		fyne.Do(func() {
			progDlg.Hide()
			fyne.Do(func() {
				showDependabotAggregateDialog(a, parent, results, thirdParty, warnings)
				if onResult != nil {
					onResult(depAlertSummary{totalAlerts: total, when: time.Now()})
				}
			})
		})
	}()
}

// fetchAlertsForTargets queries every target concurrently (bounded by
// ghConcurrency) and returns results sorted by target key for stable display.
func fetchAlertsForTargets(targets []depTarget) []depAlertResult {
	results := make([]depAlertResult, len(targets))
	sem := make(chan struct{}, ghConcurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t depTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), depAlertCallTimeout)
			defer cancel()
			alerts, err := fetchDependabotAlerts(ctx, t.host, t.owner, t.repo)
			results[i] = depAlertResult{target: t, alerts: alerts, err: err}
		}(i, t)
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool { return results[i].target.key() < results[j].target.key() })
	return results
}

// severityCounts summarises a repo's alerts as e.g. "2 critical · 1 high".
func severityCounts(alerts []dependabotAlert) string {
	var crit, high, med, low, other int
	for _, a := range alerts {
		switch severityRank(a.Severity) {
		case 0:
			crit++
		case 1:
			high++
		case 2:
			med++
		case 3:
			low++
		default:
			other++
		}
	}
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(crit, "critical")
	add(high, "high")
	add(med, "moderate")
	add(low, "low")
	add(other, "other")
	return strings.Join(parts, " · ")
}

// showDependabotAggregateDialog renders one row per scanned repo (severity
// counts + a View-alerts button), then groups everything that wasn't scanned by
// reason — so coverage gaps are explicit and, where actionable, carry advice.
// thirdParty holds the pre-skipped clones (owner isn't yours); they never hit
// the API.
func showDependabotAggregateDialog(a fyne.App, parent fyne.Window, results []depAlertResult, thirdParty []depTarget, warnings []string) {
	rows := container.NewVBox()

	total := 0
	reposWithAlerts := 0
	scanned := 0

	// Skipped repos grouped by reason, keeping the full target so each line can
	// also show the local clone path (where we found it on disk) — handy for
	// pruning third-party clones you no longer need.
	buckets := map[depErrBucket][]depTarget{}
	buckets[depErrThirdParty] = append(buckets[depErrThirdParty], thirdParty...)

	for _, r := range results {
		r := r
		if r.err != nil {
			b := classifyDepError(r.err)
			buckets[b] = append(buckets[b], r.target)
			continue
		}
		scanned++
		n := len(r.alerts)
		total += n
		repoLabelText := r.target.owner + "/" + r.target.repo
		if !strings.EqualFold(r.target.hostOrDefault(), "github.com") {
			repoLabelText = r.target.hostOrDefault() + "/" + repoLabelText
		}
		var statusText string
		if n == 0 {
			statusText = "✓ clear"
		} else {
			reposWithAlerts++
			statusText = fmt.Sprintf("⚠ %d open  (%s)", n, severityCounts(r.alerts))
		}
		nameLbl := widget.NewLabelWithStyle(repoLabelText, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		statusLbl := widget.NewLabel(statusText)
		viewBtn := widget.NewButtonWithIcon("View alerts", theme.SearchIcon(), func() {
			showDependabotAlertsDialog(parent, r.target.owner, r.target.repo, r.target.repoRoot, r.alerts)
		})
		if n == 0 {
			viewBtn.Disable()
		}
		rows.Add(container.NewBorder(nil, nil, nameLbl, viewBtn, statusLbl))
	}

	// Skipped sections, each with a heading + advice and a read-only list.
	type bucketSpec struct {
		bucket depErrBucket
		title  string
		advice string
	}
	order := []bucketSpec{
		{depErrDisabled, "Dependabot not enabled", "Enable in each repo's Settings → Code security → Dependabot alerts."},
		{depErrNotFound, "Not found / no access (404)", "Repo may be private, renamed, or not visible to your gh account."},
		{depErrAuth, "Authentication needed", "Run `gh auth login` (add `--hostname <host>` for Enterprise) then rescan."},
		{depErrThirdParty, "Third-party (no admin access)", "Locally-cloned repos owned by others — you can't read their Dependabot alerts. Paths shown so you can prune clones you no longer need."},
		{depErrNotAuthorized, "Not authorized (403)", "You're not an admin on these repos."},
		{depErrOther, "Other errors", ""},
	}
	skipped := 0
	for _, spec := range order {
		repos := buckets[spec.bucket]
		if len(repos) == 0 {
			continue
		}
		skipped += len(repos)
		sort.Slice(repos, func(i, j int) bool { return repos[i].key() < repos[j].key() })
		lines := make([]string, len(repos))
		for i, t := range repos {
			// "host/owner/repo  —  /local/clone/path" when cloned locally, so
			// untouched third-party clones are easy to locate and remove.
			line := t.hostOrDefault() + "/" + t.owner + "/" + t.repo
			if t.repoRoot != "" {
				line += "  —  " + t.repoRoot
			}
			lines[i] = line
		}
		rows.Add(widget.NewSeparator())
		heading := fmt.Sprintf("%s — %d", spec.title, len(repos))
		rows.Add(widget.NewLabelWithStyle(heading, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		if spec.advice != "" {
			adv := widget.NewLabel(spec.advice)
			adv.Wrapping = fyne.TextWrapWord
			adv.TextStyle = fyne.TextStyle{Italic: true}
			rows.Add(adv)
		}
		box := widget.NewMultiLineEntry()
		box.Wrapping = fyne.TextWrapWord
		box.SetText(strings.Join(lines, "\n"))
		bs := container.NewVScroll(box)
		bs.SetMinSize(fyne.NewSize(820, minScrollHeight(len(repos))))
		rows.Add(bs)
	}

	scroll := container.NewVScroll(rows)
	scroll.SetMinSize(fyne.NewSize(840, 460))

	hdrText := fmt.Sprintf("%d open alert(s) across %d repo(s) with findings — %d repo(s) scanned, %d skipped.",
		total, reposWithAlerts, scanned, skipped)
	if len(warnings) > 0 {
		hdrText += "\n" + strings.Join(warnings, "\n")
	}
	hdr := widget.NewLabel(hdrText)
	hdr.Wrapping = fyne.TextWrapWord

	content := container.NewBorder(container.NewVBox(hdr, widget.NewSeparator()), nil, nil, nil, scroll)
	d := dialog.NewCustom("Dependabot — All Repos", "Close", content, parent)
	d.Resize(fyne.NewSize(920, 640))
	d.Show()
}

// minScrollHeight sizes a skipped-bucket list box to its contents within sane
// bounds, so a 1-repo bucket isn't a tall empty box and a 30-repo one scrolls.
func minScrollHeight(n int) float32 {
	h := float32(n) * 22
	if h < 44 {
		h = 44
	}
	if h > 160 {
		h = 160
	}
	return h
}

// showDepAlertConfig manages the owners/orgs to enumerate and the
// include-archived toggle. Seeds the owners list from the authenticated gh
// user on first open so it isn't empty out of the box.
func showDepAlertConfig(a fyne.App, parent fyne.Window) {
	owners := loadDepAlertOwners(a)
	if len(owners) == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		if login, err := ghAuthenticatedUser(ctx); err == nil && login != "" {
			owners = []string{login}
		}
		cancel()
	}

	includeArchived := widget.NewCheck("Include archived repositories", nil)
	includeArchived.SetChecked(a.Preferences().Bool(prefDepAlertIncludeArchived))

	ownersBox := container.NewVBox()
	var rebuild func()
	rebuild = func() {
		ownersBox.RemoveAll()
		if len(owners) == 0 {
			ownersBox.Add(widget.NewLabel("No owners added yet."))
		}
		for i, o := range owners {
			i, o := i, o
			lbl := widget.NewLabel(o)
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				owners = append(owners[:i], owners[i+1:]...)
				rebuild()
			})
			del.Importance = widget.LowImportance
			ownersBox.Add(container.NewBorder(nil, nil, nil, del, lbl))
		}
		ownersBox.Refresh()
	}

	ownerEntry := widget.NewEntry()
	ownerEntry.SetPlaceHolder("GitHub user or org (e.g. amarillier)")
	addOwner := func() {
		name := strings.TrimSpace(ownerEntry.Text)
		if name == "" {
			return
		}
		for _, existing := range owners {
			if strings.EqualFold(existing, name) {
				ownerEntry.SetText("")
				return
			}
		}
		owners = append(owners, name)
		ownerEntry.SetText("")
		rebuild()
	}
	ownerEntry.OnSubmitted = func(string) { addOwner() }
	addBtn := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), addOwner)

	rebuild()

	intro := widget.NewLabel("“Dependabot: Scan All Repos” queries GitHub for open Dependabot alerts across every repo under these owners/orgs (via the gh CLI), unioned with the local repos you've configured for dep-scan. Requires gh installed and authenticated (gh auth login).")
	intro.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(
		intro,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Owners / orgs to enumerate", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		ownersBox,
		container.NewBorder(nil, nil, nil, addBtn, ownerEntry),
		widget.NewSeparator(),
		includeArchived,
	)
	scroll := container.NewVScroll(body)
	scroll.SetMinSize(fyne.NewSize(540, 360))

	d := dialog.NewCustomConfirm("Dependabot — Configure Owners", "Save", "Cancel", scroll, func(save bool) {
		if !save {
			return
		}
		saveDepAlertOwners(a, owners)
		a.Preferences().SetBool(prefDepAlertIncludeArchived, includeArchived.Checked)
	}, parent)
	d.Resize(fyne.NewSize(620, 540))
	d.Show()
}
