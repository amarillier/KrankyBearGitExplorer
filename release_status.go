package main

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// releaseStatus describes how the current HEAD relates to the repo's
// most recent semver-tagged release. Populated by detectReleaseStatus
// from local refs (no network). The UI renders the result via label()
// alongside the existing remote-sync indicator.
type releaseStatus struct {
	HasRelease   bool   // any semver-shaped tag exists
	LatestTag    string // e.g. "v0.9.3" — the tag as named in git (with leading "v" if present)
	CommitsSince int    // local commits between LatestTag and HEAD; 0 means HEAD is at the tag
}

// label renders the indicator for the explorer's header line.
func (s releaseStatus) label() string {
	if !s.HasRelease {
		return "no release tag"
	}
	if s.CommitsSince == 0 {
		return "released " + s.LatestTag
	}
	return fmt.Sprintf("↑%d since %s", s.CommitsSince, s.LatestTag)
}

// detectReleaseStatus inspects repoRoot's tags and HEAD to determine
// whether the current commit is at the most recent semver release or
// ahead of it. The anchor is the *tag* (the actual published release),
// not FyneApp.toml's Version field — if the version has been bumped
// but no tag pushed yet, the version-bump commit itself is "ahead" of
// the last release, which is exactly what the user wants to see.
//
// Cap shellouts with a short timeout so a hung index lookup can't
// freeze the header refresh.
func detectReleaseStatus(repoRoot string) releaseStatus {
	info := releaseStatus{}
	if repoRoot == "" {
		return info
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "tag", "--list", "--sort=-v:refname").Output()
	if err != nil {
		return info
	}
	tagRe := regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:[-+.][\w.-]+)?$`)
	var latest string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if tagRe.MatchString(line) {
			latest = line
			break
		}
	}
	if latest == "" {
		return info
	}
	info.HasRelease = true
	info.LatestTag = latest

	countBytes, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-list", "--count", latest+"..HEAD").Output()
	if err != nil {
		return info
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(countBytes)))
	info.CommitsSince = count
	return info
}
