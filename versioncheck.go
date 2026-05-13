package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	updatechecker "github.com/amarillier/go-update-checker"
)

const githubReleasesAPI = "https://api.github.com/repos/amarillier/KrankyBearGitExplorer/releases/latest"

// go-update-checker's cache stores only tag/name, not owner/repo; use a repo-specific
// filename so a generic latestcheck.json is never confused with another app's release.
const updateCheckStateFileName = "latestcheck-KrankyBearGitExplorer.json"

// releaseTitleLine formats GitHub tag plus optional release title for dialogs.
func releaseTitleLine(tag, title string) string {
	tag = strings.TrimSpace(tag)
	title = strings.TrimSpace(title)
	switch {
	case tag == "":
		return ""
	case title == "":
		return tag
	default:
		return fmt.Sprintf("%s (%s)", tag, title)
	}
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
}

// versionOrder compares two semver-like tags (optional leading "v", numeric dot-separated core,
// optional "-prerelease"). Returns -1 if a < b, 0 if equal, 1 if a > b.
func versionOrder(a, b string) int {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	coreA, preA := splitCorePre(a)
	coreB, preB := splitCorePre(b)
	if c := compareNumericCore(coreA, coreB); c != 0 {
		return c
	}
	return comparePrerelease(preA, preB)
}

func splitCorePre(s string) (core, pre string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '-'); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func compareNumericCore(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var na, nb int
		var okA, okB = true, true
		if i < len(pa) {
			na, okA = parseIntPrefix(pa[i])
		}
		if i < len(pb) {
			nb, okB = parseIntPrefix(pb[i])
		}
		if !okA || !okB {
			return strings.Compare(a, b)
		}
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
	}
	return 0
}

func parseIntPrefix(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.Atoi(s[:end])
	return v, err == nil
}

// Semver: a release without prerelease is newer than same core with prerelease.
func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	return strings.Compare(a, b)
}

// checkForUpdates queries GitHub and opens the update dialog on the UI thread.
func checkForUpdates(a fyne.App) {
	go func() {
		client := &http.Client{Timeout: 12 * time.Second}
		req, err := http.NewRequest(http.MethodGet, githubReleasesAPI, nil)
		if err != nil {
			fyne.Do(func() { showUpdateDialog(a, "Could not check for updates.", false, false) })
			return
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", appName+"/"+appVersion)

		resp, err := client.Do(req)
		if err != nil {
			fyne.Do(func() {
				showUpdateDialog(a, "Could not reach GitHub to check for updates.\n\n"+err.Error(), false, false)
			})
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != http.StatusOK {
			fyne.Do(func() {
				showUpdateDialog(a, fmt.Sprintf("Update check failed (%s).", resp.Status), false, false)
			})
			return
		}

		var rel ghRelease
		if err := json.Unmarshal(body, &rel); err != nil {
			fyne.Do(func() { showUpdateDialog(a, "Could not read release information.", false, false) })
			return
		}

		remote := strings.TrimSpace(rel.TagName)
		local := strings.TrimSpace(appVersion)
		var msg string
		var available bool
		var localAhead bool
		switch {
		case remote == "":
			msg = fmt.Sprintf("Could not determine the latest release tag.\n\nYour version: %s", local)
		case versionOrder(local, remote) < 0:
			available = true
			msg = fmt.Sprintf("A newer release is available.\n\nYou have: %s\nLatest: %s.", local, releaseTitleLine(rel.TagName, rel.Name))
		case versionOrder(local, remote) > 0:
			localAhead = true
			msg = fmt.Sprintf("Your build is newer than the latest release on GitHub\n(development or unpublished build).\n\nYour version: %s\nLatest release: %s", local, releaseTitleLine(rel.TagName, rel.Name))
		default:
			msg = fmt.Sprintf("You are up to date.\n\nCurrent version: %s\nLatest release: %s", local, releaseTitleLine(rel.TagName, rel.Name))
		}

		fyne.Do(func() { showUpdateDialog(a, msg, available, localAhead) })
	}()
}

// Interval between automatic update checks on startup (manual Help → Check for Updates is unchanged).
const launchUpdateCheckIntervalDays = 7

// maybeCheckUpdatesOnLaunch runs in the background after the UI is up. It uses
// github.com/amarillier/go-update-checker with a minimum interval so we do not
// call GitHub on every launch. Only opens the dialog when a newer release exists;
// network errors are silent (same idea as KrankyBearClock).
func maybeCheckUpdatesOnLaunch(a fyne.App) {
	path := fyneUpdateCheckStatePath(appID)
	if path == "" {
		return
	}
	updatechecker.SetCheckStatePath(path)
	uc := updatechecker.New(
		"amarillier",
		"KrankyBearGitExplorer",
		appName,
		"https://github.com/amarillier/KrankyBearGitExplorer/releases/latest",
		launchUpdateCheckIntervalDays,
		false,
	)
	uc.CheckForUpdate(appVersion)
	if !uc.UpdateAvailable {
		return
	}
	remoteTag := strings.TrimSpace(uc.RemoteTag)
	name := strings.TrimSpace(uc.RemoteName)
	fyne.Do(func() {
		msg := fmt.Sprintf("A newer release is available.\n\nYou have: %s\nLatest: %s.", appVersion, releaseTitleLine(remoteTag, name))
		if remoteTag == "" {
			msg = fmt.Sprintf("A newer release is available.\n\nYou have: %s", appVersion)
		}
		showUpdateDialog(a, msg, true, false)
	})
}
