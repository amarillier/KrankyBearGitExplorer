package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/go-git/go-git/v5"
)

// ghReleaseRow is one row of `gh release list --json …`.
type ghReleaseRow struct {
	TagName      string `json:"tagName"`
	Name         string `json:"name"`
	IsLatest     bool   `json:"isLatest"`
	IsDraft      bool   `json:"isDraft"`
	IsPrerelease bool   `json:"isPrerelease"`
	PublishedAt  string `json:"publishedAt"`
}

func (r ghReleaseRow) label() string {
	var flags []string
	if r.IsLatest {
		flags = append(flags, "latest")
	}
	if r.IsDraft {
		flags = append(flags, "draft")
	}
	if r.IsPrerelease {
		flags = append(flags, "pre-release")
	}
	s := r.TagName
	if r.Name != "" && r.Name != r.TagName {
		s += "  —  " + r.Name
	}
	if len(flags) > 0 {
		s += "  [" + strings.Join(flags, ", ") + "]"
	}
	return s
}

// listReleases runs `gh release list` against host/owner/repo.
func listReleases(ctx context.Context, host, owner, repoName string) ([]ghReleaseRow, error) {
	cmd := exec.CommandContext(ctx, "gh", "release", "list", "--repo", repoArg(host, owner, repoName),
		"--limit", "100", "--json", "tagName,name,isLatest,isDraft,isPrerelease,publishedAt")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "Bad credentials") || strings.Contains(msg, "authentication") || strings.Contains(msg, "401") {
			return nil, fmt.Errorf("gh CLI is not authenticated for %s — run `%s` in a terminal, then retry", host, authLoginHint(host))
		}
		return nil, fmt.Errorf("gh release list: %w\n%s", err, msg)
	}
	var rels []ghReleaseRow
	if err := json.Unmarshal(out, &rels); err != nil {
		return nil, fmt.Errorf("parse gh release list output: %w", err)
	}
	return rels, nil
}

// deleteReleaseOnly removes a release; the git tag is removed too only when
// cleanupTag is set.
func deleteReleaseOnly(ctx context.Context, host, owner, repoName, tag string, cleanupTag bool) error {
	args := []string{"release", "delete", tag, "--repo", repoArg(host, owner, repoName), "--yes"}
	if cleanupTag {
		args = append(args, "--cleanup-tag")
	}
	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// showManageReleasesDialog lists the repo's GitHub releases with checkboxes
// and deletes the ticked ones via `gh release delete` (the equivalent of
// `gh release list` / `gh release delete <tag> -y`).
func showManageReleasesDialog(parent fyne.Window, repo *git.Repository, repoRoot string) {
	if repo == nil || repoRoot == "" {
		return
	}
	host, owner, repoName, ok := findGitHubReleaseTarget(repo)
	if !ok {
		dialog.ShowError(fmt.Errorf("no github-compatible remote found for this repo — add or check remotes via Repo ▾ → Manage Remotes…"), parent)
		return
	}
	if _, err := exec.LookPath("gh"); err != nil {
		dialog.ShowError(fmt.Errorf("gh CLI not found on PATH — install from https://cli.github.com/"), parent)
		return
	}

	list := container.NewVBox()
	intro := widget.NewLabel("")
	intro.Wrapping = fyne.TextWrapWord
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	cleanupTag := widget.NewCheck("Also delete the git tag on the remote", nil)
	cleanupTag.SetChecked(true)
	deleteBtn := widget.NewButtonWithIcon("Delete selected", theme.DeleteIcon(), nil)
	deleteBtn.Importance = widget.DangerImportance
	deleteBtn.Disable()
	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), nil)
	var d *dialog.CustomDialog
	closeBtn := widget.NewButton("Close", func() { d.Hide() })

	var checks []*widget.Check
	var tags []string

	var reload func()
	reload = func() {
		intro.SetText(fmt.Sprintf("Loading releases for %s/%s…", owner, repoName))
		refreshBtn.Disable()
		deleteBtn.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rels, err := listReleases(ctx, host, owner, repoName)
			fyne.Do(func() {
				refreshBtn.Enable()
				list.RemoveAll()
				checks, tags = nil, nil
				if err != nil {
					intro.SetText("Couldn't list releases: " + err.Error())
					return
				}
				if len(rels) == 0 {
					intro.SetText(fmt.Sprintf("%s/%s has no releases.", owner, repoName))
					return
				}
				intro.SetText(fmt.Sprintf("%d release(s) in %s/%s. Tick the ones to delete.", len(rels), owner, repoName))
				for _, r := range rels {
					c := widget.NewCheck(r.label(), nil)
					date := widget.NewLabel(strings.SplitN(r.PublishedAt, "T", 2)[0])
					date.TextStyle = fyne.TextStyle{Italic: true}
					checks = append(checks, c)
					tags = append(tags, r.TagName)
					list.Add(container.NewBorder(nil, nil, nil, date, c))
				}
				deleteBtn.Enable()
			})
		}()
	}

	deleteBtn.OnTapped = func() {
		var pick []string
		for i, c := range checks {
			if c.Checked {
				pick = append(pick, tags[i])
			}
		}
		if len(pick) == 0 {
			status.SetText("Nothing selected.")
			return
		}
		withTag := cleanupTag.Checked
		msg := fmt.Sprintf("Permanently delete %d release(s) from %s/%s?\n\n%s", len(pick), owner, repoName, strings.Join(pick, "\n"))
		if withTag {
			msg += "\n\nTheir git tags will be deleted too."
		}
		dialog.ShowConfirm("Delete releases", msg, func(ok bool) {
			if !ok {
				return
			}
			deleteBtn.Disable()
			status.SetText("Deleting…")
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				deleted := 0
				var failed []string
				for _, t := range pick {
					if err := deleteReleaseOnly(ctx, host, owner, repoName, t, withTag); err != nil {
						failed = append(failed, fmt.Sprintf("%s — %v", t, err))
					} else {
						deleted++
					}
				}
				res := fmt.Sprintf("Deleted %d release(s).", deleted)
				if len(failed) > 0 {
					res += "\nCould not delete:\n" + strings.Join(failed, "\n")
				}
				fyne.Do(func() {
					status.SetText(res)
					reload()
				})
			}()
		}, parent)
	}
	refreshBtn.OnTapped = reload

	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(560, 260))
	root := container.NewBorder(intro,
		container.NewVBox(widget.NewSeparator(), cleanupTag, status,
			container.NewHBox(deleteBtn, refreshBtn, layout.NewSpacer(), closeBtn)),
		nil, nil, scroll)
	d = dialog.NewCustomWithoutButtons("Manage releases — "+filepath.Base(repoRoot), root, parent)
	d.Resize(fyne.NewSize(700, 500))
	d.Show()
	reload()
}
