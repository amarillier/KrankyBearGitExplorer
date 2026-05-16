package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// repoTreeModel holds flattened paths (slash-separated, relative to repo root) and per-file git status.
type repoTreeModel struct {
	paths      map[string]struct{}
	fileStatus map[string]string // only meaningful for file leaves; dirs may be empty

	// ignoreMatcher matches the repo's .gitignore chain (root, nested, and
	// .git/info/exclude). Used by readDirEntries to tag entries that aren't
	// in fileStatus as "ignored" — go-git's Status() omits ignored files by
	// default (matching the `git status` CLI), so without this, ignored
	// entries would render with empty status and slip past the filter bar.
	ignoreMatcher gitignore.Matcher

	// submodules is the set of slash-separated repo-relative paths declared
	// as submodules in .gitmodules. Used by readDirEntries to tag entries
	// with status "submodule" so they're visually distinct from regular
	// directories, and by the click handler to add them to recents when
	// the user descends into one (a submodule is its own repo).
	submodules map[string]struct{}

	// submoduleAncestors is the set of slash-separated repo-relative paths
	// that are ancestors of any submodule. Tagged with "contains submodule"
	// in the folder view so a vendor/ wrapper folder reads differently from
	// an unrelated plain directory before you click into it.
	submoduleAncestors map[string]struct{}

	// dirRollup aggregates non-clean child counts for every ancestor
	// directory of a non-clean file. Used to label folder rows with a
	// rolled-up status like "modified (5)" and to keep "Only dirty /
	// Only untracked" filters useful for directories — previously dirs
	// always passed the status gate because they had no status of their
	// own. Ignored files are not enumerated by go-git's Status() and are
	// deliberately omitted from the rollup; auditing ignored content is
	// already handled at the file level via the gitignore matcher.
	dirRollup map[string]rollupCounts

	// visible, when non-nil, restricts childIDs to the IDs in this set. Used
	// by the explorer's filter bar to hide rows that don't match the active
	// name / status filters. A path being present means "this node should be
	// shown" — ancestors of every visible leaf are also added so the tree
	// remains navigable.
	visible map[string]struct{}
}

// rollupCounts summarises the non-clean children under a directory.
// conflict + dirty + untracked == total non-clean files (excluding ignored).
type rollupCounts struct {
	conflict  int
	dirty     int // modified / staged / deleted / type-changed / renamed / etc.
	untracked int
}

// total returns conflict + dirty + untracked.
func (rc rollupCounts) total() int { return rc.conflict + rc.dirty + rc.untracked }

// label renders the rollup as a Status-column string. Empty when nothing
// non-clean is rolled up. Severity precedence: conflict > modified > untracked.
// The count is total non-clean files in the subtree, not just the headline
// category — so "modified (5)" can mean 3 modified + 2 untracked.
func (rc rollupCounts) label() string {
	total := rc.total()
	if total == 0 {
		return ""
	}
	severity := "untracked"
	switch {
	case rc.conflict > 0:
		severity = "conflict"
	case rc.dirty > 0:
		severity = "modified"
	}
	return fmt.Sprintf("%s (%d)", severity, total)
}

// explorerFilter captures the active filter state shared between the folder
// list and the tracked-files tree. Zero value (empty name + all bools false)
// means: hide ignored files (showIgnored=false), show everything else.
type explorerFilter struct {
	nameContains  string // lowercase substring, "" = no name filter
	onlyDirty     bool
	onlyUntracked bool
	onlyIgnored   bool
	showIgnored   bool
}

func newRepoTreeModel() *repoTreeModel {
	return &repoTreeModel{
		paths:              make(map[string]struct{}),
		fileStatus:         make(map[string]string),
		submodules:         make(map[string]struct{}),
		submoduleAncestors: make(map[string]struct{}),
		dirRollup:          make(map[string]rollupCounts),
	}
}

func (m *repoTreeModel) registerPath(rel string) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return
	}
	parts := strings.Split(rel, "/")
	acc := ""
	for _, p := range parts {
		if acc == "" {
			acc = p
		} else {
			acc = acc + "/" + p
		}
		m.paths[acc] = struct{}{}
	}
}

func formatStatus(fs *git.FileStatus) string {
	if fs.Staging == git.Unmodified && fs.Worktree == git.Unmodified {
		return "tracked"
	}
	return fmt.Sprintf("%c%c", fs.Staging, fs.Worktree)
}

func buildRepoTreeModel(repoRoot string) (*repoTreeModel, *git.Repository, error) {
	repo, err := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, nil, fmt.Errorf("open repository: %w", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, repo, fmt.Errorf("worktree: %w", err)
	}

	st, err := wt.StatusWithOptions(git.StatusOptions{Strategy: git.Preload})
	if err != nil {
		return nil, repo, fmt.Errorf("status: %w", err)
	}

	m := newRepoTreeModel()
	for path, fs := range st {
		p := filepath.ToSlash(path)
		m.registerPath(p)
		m.fileStatus[p] = formatStatus(fs)
	}

	// Roll up non-clean child counts onto every ancestor directory. Walked
	// after fileStatus is fully populated so each leaf is classified once.
	for p, raw := range m.fileStatus {
		var bump func(rc *rollupCounts)
		switch {
		case humanStatusLabel(raw) == "conflict":
			bump = func(rc *rollupCounts) { rc.conflict++ }
		case humanStatusLabel(raw) == "untracked":
			bump = func(rc *rollupCounts) { rc.untracked++ }
		case isDirtyStatus(humanStatusLabel(raw)):
			bump = func(rc *rollupCounts) { rc.dirty++ }
		}
		if bump == nil {
			continue
		}
		cur := p
		for {
			idx := strings.LastIndex(cur, "/")
			if idx < 0 {
				break
			}
			cur = cur[:idx]
			rc := m.dirRollup[cur]
			bump(&rc)
			m.dirRollup[cur] = rc
		}
	}

	if patterns, err := gitignore.ReadPatterns(wt.Filesystem, nil); err == nil {
		m.ignoreMatcher = gitignore.NewMatcher(patterns)
	}

	if subs, err := wt.Submodules(); err == nil {
		for _, s := range subs {
			if cfg := s.Config(); cfg != nil && cfg.Path != "" {
				p := filepath.ToSlash(cfg.Path)
				m.submodules[p] = struct{}{}
				for cur := p; ; {
					idx := strings.LastIndex(cur, "/")
					if idx < 0 {
						break
					}
					cur = cur[:idx]
					m.submoduleAncestors[cur] = struct{}{}
				}
			}
		}
	}

	return m, repo, nil
}

// isIgnored reports whether the slash-separated repo-relative path is excluded
// by the repo's .gitignore chain. Safe to call when the matcher couldn't be
// built — returns false in that case.
func (m *repoTreeModel) isIgnored(rel string, isDir bool) bool {
	if m.ignoreMatcher == nil || rel == "" {
		return false
	}
	return m.ignoreMatcher.Match(strings.Split(rel, "/"), isDir)
}

func (m *repoTreeModel) childIDs(parent string) []string {
	seen := make(map[string]struct{})
	var out []string
	if parent == "" {
		for p := range m.paths {
			var child string
			if idx := strings.Index(p, "/"); idx >= 0 {
				child = p[:idx]
			} else {
				child = p
			}
			if _, ok := seen[child]; !ok {
				seen[child] = struct{}{}
				out = append(out, child)
			}
		}
	} else {
		prefix := parent + "/"
		for p := range m.paths {
			if !strings.HasPrefix(p, prefix) {
				continue
			}
			rest := strings.TrimPrefix(p, prefix)
			var child string
			if idx := strings.Index(rest, "/"); idx >= 0 {
				child = parent + "/" + rest[:idx]
			} else {
				child = p
			}
			if _, ok := seen[child]; !ok {
				seen[child] = struct{}{}
				out = append(out, child)
			}
		}
	}
	sort.Strings(out)
	if m.visible != nil {
		kept := out[:0]
		for _, id := range out {
			if _, ok := m.visible[id]; ok {
				kept = append(kept, id)
			}
		}
		out = kept
	}
	return out
}

// computeVisible walks the model's paths and returns the set of node IDs that
// pass `f`. Visibility rules:
//   - A leaf (file) is visible if its status passes the "only dirty / only
//     untracked / show ignored" gate AND either its basename matches the name
//     filter OR it sits under a directory whose name matches.
//   - A directory whose own basename matches the name filter pulls in all its
//     descendants (still subject to the status gate on leaves).
//   - Every ancestor of a visible node is also marked visible so the tree is
//     navigable.
func (m *repoTreeModel) computeVisible(f explorerFilter) map[string]struct{} {
	visible := make(map[string]struct{})

	branchSet := make(map[string]struct{})
	for p := range m.paths {
		if m.isBranchRaw(p) {
			branchSet[p] = struct{}{}
		}
	}

	matchedDirs := make(map[string]struct{})
	if f.nameContains != "" {
		for p := range branchSet {
			if strings.Contains(strings.ToLower(pathBase(p)), f.nameContains) {
				matchedDirs[p] = struct{}{}
			}
		}
	}
	underMatchedDir := func(p string) bool {
		for d := range matchedDirs {
			if strings.HasPrefix(p, d+"/") {
				return true
			}
		}
		return false
	}

	mark := func(p string) {
		visible[p] = struct{}{}
		cur := p
		for {
			idx := strings.LastIndex(cur, "/")
			if idx < 0 {
				break
			}
			cur = cur[:idx]
			visible[cur] = struct{}{}
		}
	}

	for p := range m.paths {
		if _, isBranch := branchSet[p]; isBranch {
			continue
		}
		if !leafStatusPasses(m.fileStatus[p], f) {
			continue
		}
		if f.nameContains != "" {
			if !strings.Contains(strings.ToLower(pathBase(p)), f.nameContains) && !underMatchedDir(p) {
				continue
			}
		}
		mark(p)
	}

	for d := range matchedDirs {
		mark(d)
	}

	return visible
}

// isBranchRaw is isBranch without the visibility filter — used internally by
// computeVisible to classify nodes from the underlying paths set.
func (m *repoTreeModel) isBranchRaw(id string) bool {
	prefix := id + "/"
	for p := range m.paths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// leafStatusPasses returns true if a leaf file's raw status code clears the
// "only dirty / only untracked / only ignored / show ignored" gate. The name
// filter is applied separately by the caller.
//
// "Only ignored" implicitly bypasses the show-ignored gate — otherwise the
// filter would silently match zero files. The "Only X" toggles combine as an
// OR-union when more than one is active.
func leafStatusPasses(raw string, f explorerFilter) bool {
	human := humanStatusLabel(raw)
	if !f.showIgnored && !f.onlyIgnored && human == "ignored" {
		return false
	}
	if f.onlyDirty || f.onlyUntracked || f.onlyIgnored {
		match := false
		if f.onlyDirty && isDirtyStatus(human) {
			match = true
		}
		if f.onlyUntracked && human == "untracked" {
			match = true
		}
		if f.onlyIgnored && human == "ignored" {
			match = true
		}
		if !match {
			return false
		}
	}
	return true
}

// isDirtyStatus reports whether a human-readable status label represents a
// modification vs HEAD. "untracked" and "ignored" have their own toggles and
// are deliberately excluded.
func isDirtyStatus(human string) bool {
	switch human {
	case "", "tracked", "untracked", "ignored":
		return false
	}
	if strings.HasPrefix(human, "← ") {
		return false
	}
	return true
}

func pathBase(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

func (m *repoTreeModel) isBranch(id string) bool {
	prefix := id + "/"
	for p := range m.paths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func countInterestingFiles(st git.Status) (clean, dirty int) {
	for _, fs := range st {
		if fs.Staging == git.Unmodified && fs.Worktree == git.Unmodified {
			clean++
		} else {
			dirty++
		}
	}
	return clean, dirty
}

func shortBranchName(repo *git.Repository) string {
	head, err := repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return "(no commits)"
		}
		return "unknown"
	}
	return head.Name().Short()
}
