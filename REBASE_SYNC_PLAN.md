# Plan: rebase-aware one-click Sync + chained pull-then-push recovery

> Draft authored from the TaniumQuest window for the agent working in this repo.
> Verify the current code before implementing — line numbers below are from the
> state on 2026-06-04 and may drift.

## Motivation

When a repo has automation (CI bots), a second machine, or collaborators pushing
to the same branch, the local branch routinely falls *behind* the remote, and a
plain `git push` is rejected non-fast-forward. The correct fix is almost always
`git pull --rebase` then push. Today this tool surfaces the rejection well, but
the recovery is a multi-step manual dance. We want a GitHub-Desktop-style
**Sync** (rebase-flavored) and a smoother rejection recovery.

This complements `git config pull.rebase true` / `rebase.autoStash true`, which
the user now has set globally — so a bare `git pull` already rebases. The tool's
existing `runPull(repoRoot, useRebase)` passes an *explicit* `--rebase` /
`--no-rebase`, so it overrides that config per-pull (intended) — keep that.

## What already exists (build on these — do NOT duplicate)

- `runPush(repoRoot, remote, branch, setUpstream)` — git_management.go ~1507
- `runForcePush(...)` + `pushNeedsForce(output)` (`"non-fast-forward"` detector) — ~1528/1542
- `runPull(repoRoot, useRebase)` (explicit `--rebase`/`--no-rebase`) — ~1903
- `pullRebaseDefault(repoRoot)` (reads `pull.rebase`, local>global) — ~1889
- `showPushDialog(...)` with an **inline rescue row**: a "Pull first" button
  (`onPullNeeded`) + "Force push (--force-with-lease)" — ~1665-1833
- `showPullDialog(...)` with a "Rebase instead of merge" checkbox seeded from
  `pullRebaseDefault` — ~1924
- `readLocalSync(repoRoot) remoteSyncInfo` (cached, no network): `Ahead`,
  `Behind`, `HasUpstream`, `UpstreamRef`, `label()` — remote_sync.go
- Wiring: `explorerView.pushChanges()` / `pullChanges()` — explorer_view.go
  ~1645-1668. `onPullNeeded` is `func() { v.pullChanges() }` — it opens the
  *separate* Pull dialog and does **not** retry the push afterward.

## The gaps

1. **No chained recovery.** After a non-fast-forward rejection, "Pull first"
   opens the Pull dialog; the user completes it, closes it, then must re-open/
   re-click Push. Three steps for what should be one.
2. **No one-click Sync.** There's no single affordance that does
   `pull --rebase` then `push` for the common "I'm behind, integrate and
   publish" case.
3. **Purely reactive.** The Push dialog only learns it's behind by *failing*.
   It could detect `Behind > 0` up front (via `readLocalSync`) and steer the
   user to Sync before the rejection.

## Proposed changes

### A. Chain the "Pull first" rescue back into a push retry (smallest win)

In `showPushDialog`, after a successful rescue-pull, automatically re-attempt
the original push instead of leaving the user to do it.

Options (pick per the dialog's structure):
- Preferred: add an optional `onPullThenRetry func(done func(ok bool))` style
  callback, OR have the rescue run pull *in-dialog* (reuse `runPull` with
  `useRebase=true`) and on success call the existing push goroutine path again.
  Keeping the pull in-dialog (not bouncing to `showPullDialog`) makes the retry
  trivial and keeps the user in one place.
- Default the rescue-pull to **rebase** (`runPull(repoRoot, true)`) since that's
  the correct fix for the someone-else-pushed case and avoids merge bubbles.
- If the pull reports conflicts (non-zero exit, output contains `CONFLICT` /
  `could not apply`), do **not** retry the push — surface the output verbatim
  and tell the user to resolve in their editor/CLI (matches existing no-resolver
  policy). Leave force-with-lease available for the amend-already-pushed case.

### B. One-click "Sync" (pull --rebase → push)

Add a `runSync` helper and a Sync affordance:

- `func runSync(repoRoot, remote, branch string) (output string, err error)`:
  run `runPull(repoRoot, true)`; if it fails (esp. conflicts) return early with
  output; else run `runPush(repoRoot, remote, branch, false)` and concatenate
  output. Keep it as plain sequential shellouts for readable combined output.
- Surface it where the user decides to publish. Two candidates:
  - A **"Sync (pull --rebase + push)"** button in the Push dialog, shown/enabled
    when `Behind > 0` (or always, secondary to Push).
  - The header remote-sync indicator (`remoteSyncInfo.label()` shows `↑n ↓m`) —
    make the `↓`/diverged state clickable to launch Sync.
- Reuse the existing rescue/conflict handling: if the embedded push still gets
  rejected (rare race), fall through to the same rescue row.

### C. Proactive behind-detection in the Push dialog (nice-to-have)

On `showPushDialog` open, call `readLocalSync(repoRoot)`. If `Behind > 0`,
pre-show a hint ("You're ↓N behind {upstream}; Sync to rebase + push") and make
Sync the primary action. This turns the rejection into a rare fallback rather
than the normal path. `readLocalSync` is cached/no-network, so it's cheap; an
optional `checkRemoteReachable` probe can refine staleness but isn't required.

## Out of scope / non-goals

- No in-app conflict resolver — conflicts drop to the editor/CLI (unchanged).
- Don't auto-force-push from Sync; force-with-lease stays an explicit, separate,
  DangerImportance action behind a confirm (as today).
- Don't change `runPull`'s explicit-flag behavior or override user config.

## Risks / edge cases

- **Rebase conflicts mid-Sync** → must stop before push; never push a
  half-rebased tree. Detect via exit code + output markers.
- **autoStash interaction**: with `rebase.autoStash true`, a dirty tree is
  stashed/popped automatically; verify the pop didn't conflict before pushing.
- **No upstream / detached HEAD**: Sync should refuse gracefully (the Pull
  dialog already guards on upstream; mirror that).
- **Unborn HEAD / brand-new branch**: keep the existing `setUpstream` path; Sync
  only applies once an upstream exists.

## Suggested verification

- Manual: clone a repo, push a commit from a second clone (simulate bot), make a
  local commit, hit Sync → expect rebase + push, linear history, no merge bubble.
- Conflict path: create a conflicting change on both sides → Sync stops after
  pull with conflict output, push not attempted, force-with-lease still offered.
- `go build ./...` + `go vet ./...`; add/extend a small unit test around any new
  pure helper (e.g. a conflict-detection predicate like the existing
  `pushNeedsForce`).
