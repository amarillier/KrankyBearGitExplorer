package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// remoteSyncInfo describes a branch's relationship to its remote-tracking
// upstream. Filled in by readLocalSync from the cached refs (no network);
// extended in place by checkRemoteReachable when the live ls-remote check
// completes.
type remoteSyncInfo struct {
	HasUpstream bool
	HasAnyRemote bool
	UpstreamRef string // e.g. "origin/main" — empty if HasUpstream is false
	Ahead       int    // local commits not in upstream's cached tip
	Behind      int    // upstream-cached commits not in local

	// Live-check fields. Populated by checkRemoteReachable; the UI uses
	// LiveChecked to decide whether to render a status suffix.
	LiveChecked bool
	Reachable   bool
	LiveErr     error
}

// label renders the indicator for the explorer's header line. The cached
// ahead/behind state is always shown; the live-check result is appended
// only when LiveChecked is true.
func (r remoteSyncInfo) label() string {
	if !r.HasAnyRemote {
		return "(no remotes)"
	}
	if !r.HasUpstream {
		return "no upstream"
	}
	base := ""
	switch {
	case r.Ahead == 0 && r.Behind == 0:
		base = "in sync with " + r.UpstreamRef
	case r.Ahead > 0 && r.Behind == 0:
		base = fmt.Sprintf("↑%d vs %s", r.Ahead, r.UpstreamRef)
	case r.Ahead == 0 && r.Behind > 0:
		base = fmt.Sprintf("↓%d vs %s", r.Behind, r.UpstreamRef)
	default:
		base = fmt.Sprintf("↑%d ↓%d vs %s", r.Ahead, r.Behind, r.UpstreamRef)
	}
	if !r.LiveChecked {
		return base
	}
	if r.Reachable {
		return base + " ✓"
	}
	return base + " — remote unreachable"
}

// readLocalSync inspects the cached refs in repoRoot to determine the
// branch's relationship to its upstream — fast, never touches the network.
// Returns a populated remoteSyncInfo even when no upstream exists; the
// caller renders the result via label().
//
// Three shellouts:
//   - `git remote` to learn whether any remote is configured at all.
//   - `git rev-parse --abbrev-ref @{upstream}` to resolve the tracking
//     ref's friendly name (exits non-zero with no upstream).
//   - `git rev-list --left-right --count @{upstream}...HEAD` for the
//     behind/ahead counts (left=upstream, right=HEAD).
func readLocalSync(repoRoot string) remoteSyncInfo {
	info := remoteSyncInfo{}
	if repoRoot == "" {
		return info
	}

	if out, err := exec.Command("git", "-C", repoRoot, "remote").Output(); err == nil {
		info.HasAnyRemote = strings.TrimSpace(string(out)) != ""
	}
	if !info.HasAnyRemote {
		return info
	}

	upstreamBytes, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "@{upstream}").Output()
	if err != nil {
		return info // no upstream is the common case for feature branches
	}
	upstream := strings.TrimSpace(string(upstreamBytes))
	if upstream == "" {
		return info
	}
	info.HasUpstream = true
	info.UpstreamRef = upstream

	countBytes, err := exec.Command("git", "-C", repoRoot, "rev-list", "--left-right", "--count", "@{upstream}...HEAD").Output()
	if err != nil {
		return info
	}
	parts := strings.Fields(string(countBytes))
	if len(parts) >= 2 {
		info.Behind, _ = strconv.Atoi(parts[0])
		info.Ahead, _ = strconv.Atoi(parts[1])
	}
	return info
}

// checkRemoteReachable runs `git ls-remote --heads <remote>` under a
// context deadline to probe whether the remote answers. Read-only — no
// fetch, no ref updates. Caller is responsible for marshalling the result
// back onto the UI thread.
func checkRemoteReachable(ctx context.Context, repoRoot, upstreamRef string) (bool, error) {
	if repoRoot == "" || upstreamRef == "" {
		return false, fmt.Errorf("missing repo or upstream")
	}
	remote := strings.SplitN(upstreamRef, "/", 2)[0]
	if remote == "" {
		return false, fmt.Errorf("malformed upstream ref %q", upstreamRef)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "ls-remote", "--heads", remote)
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return false, fmt.Errorf("ls-remote timed out — remote unreachable")
		}
		return false, err
	}
	return true, nil
}

// remoteCheckTimeout caps the live ls-remote probe. Long enough for a
// real auth handshake on a slow link, short enough that a hung VPN
// doesn't leave the indicator pending forever.
const remoteCheckTimeout = 5 * time.Second
