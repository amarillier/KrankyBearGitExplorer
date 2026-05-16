package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/go-git/go-git/v5"
)

// contributorStats summarises one author's footprint on a repo. Keyed by
// email in the aggregator so the same person committing under several
// display-name variants stays merged.
type contributorStats struct {
	Name        string
	Email       string
	Commits     int
	First, Last int64 // unix timestamps; First is the oldest commit by this author, Last the newest
	Adds        int
	Dels        int
}

// gatherContributors walks the full commit log via repo.Log and aggregates
// per-author counts + lines changed via go-git's per-commit Stats(). Synchronous
// — fine for typical-size repos. A future batch can wrap this in a progress
// dialog if it ever starts to bite.
func gatherContributors(repo *git.Repository) ([]contributorStats, error) {
	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer iter.Close()

	agg := make(map[string]*contributorStats)
	for {
		c, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("walk log: %w", err)
		}

		email := c.Author.Email
		if email == "" {
			email = c.Author.Name // worst-case key fallback
		}
		cs, ok := agg[email]
		if !ok {
			cs = &contributorStats{Name: c.Author.Name, Email: email}
			agg[email] = cs
		}
		// Keep a stable display name — first seen wins. Real-world commits
		// often vary case + middle-initial + ".".
		if cs.Name == "" {
			cs.Name = c.Author.Name
		}
		cs.Commits++
		when := c.Author.When.Unix()
		if cs.First == 0 || when < cs.First {
			cs.First = when
		}
		if when > cs.Last {
			cs.Last = when
		}

		// Lines-touched: go-git computes the diff against parent for us.
		// Errors are silently swallowed — a commit we can't stat still
		// counts; we just won't have line numbers for it.
		if stats, err := c.Stats(); err == nil {
			for _, fs := range stats {
				cs.Adds += fs.Addition
				cs.Dels += fs.Deletion
			}
		}
	}

	out := make([]contributorStats, 0, len(agg))
	for _, cs := range agg {
		out = append(out, *cs)
	}
	// Sort by commit count descending; tiebreak by lower-cased name for
	// stability across runs.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Commits != out[j].Commits {
			return out[i].Commits > out[j].Commits
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Column widths for the Contributors dialog. The Author column (which
// includes the email address and so can run long) takes the remaining
// space via a Border layout; the numeric / date columns are fixed.
const (
	contribColCommitsWidth = float32(80)
	contribColDateWidth    = float32(100)
	contribColLinesWidth   = float32(90)
)

// contributorRowWidget mirrors refRowWidget — typed label references so
// the list's update callback doesn't rely on container child ordering.
type contributorRowWidget struct {
	widget.BaseWidget
	author, commits, first, last, adds, dels *widget.Label
}

func newContributorRowWidget() *contributorRowWidget {
	mk := func() *widget.Label {
		l := widget.NewLabel("")
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	r := &contributorRowWidget{
		author:  mk(),
		commits: mk(),
		first:   mk(),
		last:    mk(),
		adds:    mk(),
		dels:    mk(),
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *contributorRowWidget) CreateRenderer() fyne.WidgetRenderer {
	content := container.NewBorder(nil, nil,
		nil,
		container.NewHBox(
			container.NewMax(sizingRect(contribColCommitsWidth), r.commits),
			container.NewMax(sizingRect(contribColDateWidth), r.first),
			container.NewMax(sizingRect(contribColDateWidth), r.last),
			container.NewMax(sizingRect(contribColLinesWidth), r.adds),
			container.NewMax(sizingRect(contribColLinesWidth), r.dels),
		),
		r.author,
	)
	return widget.NewSimpleRenderer(content)
}

// showContributorsDialog renders the per-author breakdown produced by
// gatherContributors. Pure display; no actions on the rows.
func showContributorsDialog(repo *git.Repository, parent fyne.Window, repoRoot string) {
	if repo == nil {
		return
	}
	rows, err := gatherContributors(repo)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}

	mkHeader := func(text string) *widget.Label {
		l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	header := container.NewBorder(nil, nil,
		nil,
		container.NewHBox(
			container.NewMax(sizingRect(contribColCommitsWidth), mkHeader("Commits")),
			container.NewMax(sizingRect(contribColDateWidth), mkHeader("First")),
			container.NewMax(sizingRect(contribColDateWidth), mkHeader("Last")),
			container.NewMax(sizingRect(contribColLinesWidth), mkHeader("+lines")),
			container.NewMax(sizingRect(contribColLinesWidth), mkHeader("−lines")),
		),
		mkHeader("Author"),
	)

	list := widget.NewList(
		func() int { return len(rows) },
		func() fyne.CanvasObject { return newContributorRowWidget() },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(rows) {
				return
			}
			r := rows[id]
			row := o.(*contributorRowWidget)
			row.author.SetText(authorDisplay(r))
			row.commits.SetText(fmt.Sprintf("%d", r.Commits))
			row.first.SetText(unixToDateString(r.First))
			row.last.SetText(unixToDateString(r.Last))
			row.adds.SetText(fmt.Sprintf("%d", r.Adds))
			row.dels.SetText(fmt.Sprintf("%d", r.Dels))
		},
	)

	totalCommits := 0
	totalAdds := 0
	totalDels := 0
	for _, r := range rows {
		totalCommits += r.Commits
		totalAdds += r.Adds
		totalDels += r.Dels
	}
	footer := widget.NewLabel(fmt.Sprintf("%d contributor(s) · %d commits · +%d / −%d lines",
		len(rows), totalCommits, totalAdds, totalDels))

	body := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		footer,
		nil, nil,
		list,
	)
	body.Resize(fyne.NewSize(720, 480))

	d := dialog.NewCustom("Contributors — "+filepath.Base(repoRoot), "Close", body, parent)
	d.Resize(fyne.NewSize(760, 540))
	d.Show()
}

// authorDisplay renders "Name <email>" when both are present, "Name" or
// "<email>" alone when only one is. Falls back to "(unknown)" when both
// are empty (defensive — shouldn't happen for real commits).
func authorDisplay(c contributorStats) string {
	switch {
	case c.Name != "" && c.Email != "" && c.Name != c.Email:
		return fmt.Sprintf("%s <%s>", c.Name, c.Email)
	case c.Name != "":
		return c.Name
	case c.Email != "":
		return c.Email
	default:
		return "(unknown)"
	}
}

func unixToDateString(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02")
}
