package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// repoTreeModel holds flattened paths (slash-separated, relative to repo root) and per-file git status.
type repoTreeModel struct {
	paths      map[string]struct{}
	fileStatus map[string]string // only meaningful for file leaves; dirs may be empty
}

func newRepoTreeModel() *repoTreeModel {
	return &repoTreeModel{
		paths:      make(map[string]struct{}),
		fileStatus: make(map[string]string),
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

	return m, repo, nil
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
	return out
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
