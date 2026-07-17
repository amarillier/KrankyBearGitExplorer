package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// repo_visibility.go answers a question git itself has no concept of:
// whether a repo's GitHub/GHES remote is public, private, or (GHES/GHEC)
// internal. That's purely an API property, so checking it means shelling
// out to gh — this file is the one place that call lives, reused by both
// the main window header badge and the "Audit Local Repos…" sweep.

// repoVisibility classifies the result of a visibility check.
type repoVisibility int

const (
	// visibilityUnknown means no recognised GitHub/GHES remote was found —
	// the normal case for a local-only repo, not a failure.
	visibilityUnknown repoVisibility = iota
	visibilityPublic
	visibilityPrivate
	visibilityInternal
	// visibilityCheckFailed means a GitHub/GHES remote was found but the gh
	// call itself errored (not installed, not authenticated, network, 404).
	visibilityCheckFailed
)

// repoVisibilityInfo is the outcome of checking one repo's visibility.
type repoVisibilityInfo struct {
	State             repoVisibility
	Host, Owner, Repo string
	Err               error
}

// label renders the indicator for the explorer's header line, in the same
// style as remoteSyncInfo.label() and releaseStatus.label().
func (v repoVisibilityInfo) label() string {
	switch v.State {
	case visibilityPublic:
		return "🌐 public"
	case visibilityPrivate:
		return "🔒 private"
	case visibilityInternal:
		return "🏢 internal"
	case visibilityCheckFailed:
		return "⚠ visibility check failed"
	default:
		return ""
	}
}

// checkRepoVisibility asks gh for the visibility of host/owner/repoName via
// `gh repo view --json visibility`, which works against both github.com and
// any GHES host gh is authenticated to (repoArg host-prefixes the --repo
// value the same way the release-publishing flow does).
func checkRepoVisibility(ctx context.Context, host, owner, repoName string) (repoVisibilityInfo, error) {
	info := repoVisibilityInfo{Host: host, Owner: owner, Repo: repoName}
	out, err := ghOutput(ctx, "repo", "view", repoArg(host, owner, repoName), "--json", "visibility", "-q", ".visibility")
	if err != nil {
		info.State = visibilityCheckFailed
		info.Err = err
		return info, err
	}
	switch v := strings.ToUpper(strings.TrimSpace(string(out))); v {
	case "PUBLIC":
		info.State = visibilityPublic
	case "PRIVATE":
		info.State = visibilityPrivate
	case "INTERNAL":
		info.State = visibilityInternal
	default:
		info.State = visibilityCheckFailed
		info.Err = fmt.Errorf("gh repo view returned unexpected visibility %q", v)
		return info, info.Err
	}
	return info, nil
}

// visibilityCheckTimeout caps a single-repo `gh repo view` call — matches
// the timeout used for other single-repo gh calls (e.g. ghAuthenticatedUser).
const visibilityCheckTimeout = 8 * time.Second
