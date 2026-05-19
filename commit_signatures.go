package main

import (
	"bufio"
	"bytes"
	"os/exec"

	"github.com/go-git/go-git/v5/plumbing"
)

// Signature status codes as emitted by `git log --format=%G?`. Mirrored
// verbatim from git docs so future-us can map any new codes git invents
// without spelunking through the source.
const (
	sigGood             = 'G' // valid signature
	sigBad              = 'B' // bad signature (signature didn't verify)
	sigGoodUnknownKey   = 'U' // good signature with unknown validity
	sigExpired          = 'X' // good signature that has expired
	sigKeyExpired       = 'Y' // good signature made by an expired key
	sigKeyRevoked       = 'R' // good signature made by a revoked key
	sigCannotCheck      = 'E' // signature cannot be checked (e.g. missing key)
	sigNone             = 'N' // no signature
)

// gatherCommitSignatures runs `git log --format=%H %G?` over the full repo
// log and returns sha → status-code mapping. Used by the Repo History
// window to render per-commit signature badges and the detail pane's
// signature line.
//
// Falls back to a nil map (no error) when `git` isn't on PATH or the
// command otherwise fails — the history view degrades gracefully to "no
// badges shown", which matches how stash_view handles a missing git CLI.
func gatherCommitSignatures(repoRoot string) map[plumbing.Hash]byte {
	if repoRoot == "" {
		return nil
	}
	cmd := exec.Command("git", "-C", repoRoot, "log", "--all", "--format=%H %G?")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil
	}
	out := make(map[plumbing.Hash]byte, 256)
	scanner := bufio.NewScanner(&stdout)
	// Lines are short (40-char SHA + space + 1 char) but raise the buffer
	// in case git's pretty format ever wraps weirdly on a future version.
	scanner.Buffer(make([]byte, 256), 256)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 42 { // 40 SHA + space + 1 status
			continue
		}
		shaStr := line[:40]
		status := line[41]
		hash := plumbing.NewHash(shaStr)
		if hash.IsZero() {
			continue
		}
		out[hash] = status
	}
	return out
}

// signatureBadge returns a compact glyph for the commit list row gutter —
// just enough visual signal that something's there, full detail surfaces
// in the detail pane via signatureHumanLabel. Returns a space for unsigned
// (the common case) so the column width stays stable across rows.
func signatureBadge(status byte) string {
	switch status {
	case sigGood:
		return "✓"
	case sigBad, sigExpired, sigKeyExpired, sigKeyRevoked:
		return "✗"
	case sigGoodUnknownKey, sigCannotCheck:
		return "?"
	default: // sigNone, zero, unknown
		return " "
	}
}

// signatureHumanLabel renders the detail-pane "Signature:" line. Returns
// an empty string for unsigned commits so the caller can hide the row
// entirely rather than displaying "Signature: unsigned" on every commit
// in a repo that never signs anything.
func signatureHumanLabel(status byte) string {
	switch status {
	case sigGood:
		return "Signature: ✓ valid"
	case sigBad:
		return "Signature: ✗ bad (did not verify)"
	case sigGoodUnknownKey:
		return "Signature: ? signed (unknown signing key)"
	case sigExpired:
		return "Signature: ✗ signed (signature expired)"
	case sigKeyExpired:
		return "Signature: ✗ signed (signing key expired)"
	case sigKeyRevoked:
		return "Signature: ✗ signed (signing key revoked)"
	case sigCannotCheck:
		return "Signature: ? cannot verify (no key available)"
	default:
		return ""
	}
}

