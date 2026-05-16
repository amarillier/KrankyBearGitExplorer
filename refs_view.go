package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// refRow is one entry rendered in a branches/tags listing. For lightweight
// tags `target` is the commit hash directly; for annotated tags it's the
// commit the tag object points to. `when` is the committer time of that
// commit, used for sorting newest-first.
type refRow struct {
	name    string
	sha     string // short SHA (7 chars) of the target commit, "" if unresolved
	when    string // "YYYY-MM-DD" or "" if unresolved
	subject string // first line of the target commit's message, "" if unresolved
	sortKey int64  // unix timestamp of target commit for sort; 0 when unresolved
}

// showBranchesDialog renders the repo's local branches in a sortable-ish
// list. Pure read-only; no checkout / create / delete affordances on
// purpose — that line stays with `git` itself.
func showBranchesDialog(repo *git.Repository, parent fyne.Window, repoRoot string) {
	if repo == nil {
		return
	}
	rows := collectRefRows(repo, refSourceBranches)
	sortRefRowsByDateDesc(rows)
	title := "Branches — " + filepath.Base(repoRoot)
	showRefDialog(title, []string{"Name", "Commit", "Date", "Subject"}, rows, parent)
}

// showTagsDialog renders the repo's tags. Annotated and lightweight tags
// are both shown; the target commit's date is used for sort order.
func showTagsDialog(repo *git.Repository, parent fyne.Window, repoRoot string) {
	if repo == nil {
		return
	}
	rows := collectRefRows(repo, refSourceTags)
	sortRefRowsByDateDesc(rows)
	title := "Tags — " + filepath.Base(repoRoot)
	showRefDialog(title, []string{"Name", "Commit", "Date", "Subject"}, rows, parent)
}

// showRemotesDialog renders the repo's configured remotes and their URLs.
// A remote can have multiple URLs (e.g. separate fetch/push URLs); each
// appears as its own row so paths stay aligned in the column layout.
func showRemotesDialog(repo *git.Repository, parent fyne.Window, repoRoot string) {
	if repo == nil {
		return
	}
	remotes, err := repo.Remotes()
	if err != nil {
		dialog.ShowError(fmt.Errorf("read remotes: %w", err), parent)
		return
	}
	var rows []refRow
	for _, r := range remotes {
		cfg := r.Config()
		if cfg == nil {
			continue
		}
		if len(cfg.URLs) == 0 {
			rows = append(rows, refRow{name: cfg.Name})
			continue
		}
		for _, u := range cfg.URLs {
			// Reuse the rendering pipeline by stuffing the URL into the
			// subject column; the header labels are tailored below so the
			// user reads "Name | URL" not "Name | … | Subject".
			rows = append(rows, refRow{name: cfg.Name, subject: u})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
	})
	title := "Remotes — " + filepath.Base(repoRoot)
	showRefDialog(title, []string{"Name", "URL"}, rows, parent)
}

type refSourceKind int

const (
	refSourceBranches refSourceKind = iota
	refSourceTags
)

// collectRefRows walks the iterator returned by repo.Branches / repo.Tags,
// resolves each ref to its underlying commit (transparently handling
// annotated tags), and produces a uniform refRow per entry.
func collectRefRows(repo *git.Repository, kind refSourceKind) []refRow {
	var iter interface {
		ForEach(func(*plumbing.Reference) error) error
		Close()
	}
	switch kind {
	case refSourceBranches:
		it, err := repo.Branches()
		if err != nil {
			return nil
		}
		iter = it
	case refSourceTags:
		it, err := repo.Tags()
		if err != nil {
			return nil
		}
		iter = it
	default:
		return nil
	}
	defer iter.Close()

	var rows []refRow
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		row := refRow{name: ref.Name().Short()}
		hash := ref.Hash()
		// For annotated tags the ref hash points at the tag object. Try to
		// resolve through it first; if that fails (lightweight tag or
		// branch), the hash already IS a commit.
		commitHash := hash
		if kind == refSourceTags {
			if tag, err := repo.TagObject(hash); err == nil {
				commitHash = tag.Target
			}
		}
		if c, err := repo.CommitObject(commitHash); err == nil {
			sha := commitHash.String()
			if len(sha) > 7 {
				sha = sha[:7]
			}
			row.sha = sha
			row.when = c.Committer.When.Format("2006-01-02")
			row.subject = firstLine(c.Message)
			row.sortKey = c.Committer.When.Unix()
		}
		rows = append(rows, row)
		return nil
	})
	return rows
}

func sortRefRowsByDateDesc(rows []refRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		// Unresolved (sortKey == 0) sink to the bottom; otherwise newest
		// first by commit date.
		if rows[i].sortKey == 0 && rows[j].sortKey != 0 {
			return false
		}
		if rows[i].sortKey != 0 && rows[j].sortKey == 0 {
			return true
		}
		return rows[i].sortKey > rows[j].sortKey
	})
}

// Column widths for the branches/tags/remotes dialogs. The wide column
// (Subject for branches/tags, URL for remotes) fills the remaining space
// via a Border layout — equal-width grids were spending too much room on
// the narrow SHA / Date / Name columns and truncating the useful content.
const (
	refColNameWidth = float32(180)
	refColSHAWidth  = float32(80)
	refColDateWidth = float32(100)
)

// refRowWidget is the per-row UI element used by the branches/tags/remotes
// list. Holding typed references to its labels lets the update callback
// avoid walking container.Objects, which is brittle: Fyne's Border layout
// appends center children first then top/bottom/left/right, and the order
// has shifted between releases.
type refRowWidget struct {
	widget.BaseWidget
	ncols                  int // 4 for branches/tags, 2 for remotes
	name, sha, date, wide  *widget.Label
}

func newRefRowWidget(ncols int) *refRowWidget {
	mk := func() *widget.Label {
		l := widget.NewLabel("")
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	r := &refRowWidget{
		ncols: ncols,
		name:  mk(),
		wide:  mk(),
	}
	if ncols == 4 {
		r.sha = mk()
		r.date = mk()
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *refRowWidget) CreateRenderer() fyne.WidgetRenderer {
	var content fyne.CanvasObject
	switch r.ncols {
	case 4:
		content = container.NewBorder(nil, nil,
			container.NewHBox(
				container.NewMax(sizingRect(refColNameWidth), r.name),
				container.NewMax(sizingRect(refColSHAWidth), r.sha),
				container.NewMax(sizingRect(refColDateWidth), r.date),
			),
			nil,
			r.wide,
		)
	case 2:
		content = container.NewBorder(nil, nil,
			container.NewMax(sizingRect(refColNameWidth), r.name),
			nil,
			r.wide,
		)
	default:
		content = container.NewHBox(r.name, r.wide)
	}
	return widget.NewSimpleRenderer(content)
}

// showRefDialog renders the modal-attached dialog with a header row +
// scrolling list. Column count is inferred from the headers slice
// (2 = Name/URL for remotes, 4 = Name/Commit/Date/Subject otherwise).
func showRefDialog(title string, headers []string, rows []refRow, parent fyne.Window) {
	header := refHeaderRow(headers)

	list := widget.NewList(
		func() int { return len(rows) },
		func() fyne.CanvasObject { return newRefRowWidget(len(headers)) },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id < 0 || id >= len(rows) {
				return
			}
			r := rows[id]
			row := o.(*refRowWidget)
			switch row.ncols {
			case 4:
				row.name.SetText(r.name)
				row.sha.SetText(r.sha)
				row.date.SetText(r.when)
				row.wide.SetText(r.subject)
			case 2:
				row.name.SetText(r.name)
				row.wide.SetText(r.subject) // subject column carries the URL for remotes
			}
		},
	)

	footer := widget.NewLabel(fmt.Sprintf("%d item(s)", len(rows)))
	body := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		footer,
		nil, nil,
		list,
	)
	body.Resize(fyne.NewSize(680, 480))

	d := dialog.NewCustom(title, "Close", body, parent)
	d.Resize(fyne.NewSize(820, 540))
	d.Show()
}

// refHeaderRow builds the header row matching the layout produced by
// newRefRowWidget. Bold labels, fixed-width narrow columns on the left,
// the wide column filling the rest.
func refHeaderRow(headers []string) fyne.CanvasObject {
	mk := func(text string) *widget.Label {
		l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	switch len(headers) {
	case 4: // Branches / Tags
		return container.NewBorder(nil, nil,
			container.NewHBox(
				container.NewMax(sizingRect(refColNameWidth), mk(headers[0])),
				container.NewMax(sizingRect(refColSHAWidth), mk(headers[1])),
				container.NewMax(sizingRect(refColDateWidth), mk(headers[2])),
			),
			nil,
			mk(headers[3]),
		)
	case 2: // Remotes
		return container.NewBorder(nil, nil,
			container.NewMax(sizingRect(refColNameWidth), mk(headers[0])),
			nil,
			mk(headers[1]),
		)
	}
	return container.NewGridWithColumns(len(headers))
}
