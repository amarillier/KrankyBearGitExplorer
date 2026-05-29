package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// gitLogPatchPresets are the N values offered by the dialog's selector.
// Mirrors what people actually want at the shell — peek at the last
// commit (1), recent batch (5/10), or a broader sweep (25/100). Picking
// a literal upper bound from a dropdown is friendlier than a number
// entry, and the cap is a deliberate guardrail against accidental
// "show me everything" runs on big repos.
var gitLogPatchPresets = []string{"1", "5", "10", "25", "100"}

// showGitLogPatchView dumps `git log -p -n N` output into a read-only
// monospace viewer. When path is "" the view is repo-wide; otherwise
// it scopes to a single path (`git log -p -n N -- <path>`). The N
// selector refetches on change.
func showGitLogPatchView(parent fyne.Window, repoRoot, path string) {
	if repoRoot == "" {
		return
	}

	output := widget.NewMultiLineEntry()
	output.TextStyle = fyne.TextStyle{Monospace: true}
	output.Wrapping = fyne.TextWrapOff

	nSelect := widget.NewSelect(gitLogPatchPresets, nil)
	nSelect.Selected = "1"

	fetch := func() {
		n := nSelect.Selected
		if n == "" {
			n = "1"
		}
		output.SetText("Fetching git log -p -n " + n + "…")
		go func(n, path string) {
			text, err := runGitLogPatch(repoRoot, n, path)
			fyne.Do(func() {
				if err != nil {
					output.SetText("git log -p -n " + n + " failed:\n\n" + err.Error())
					return
				}
				if text == "" {
					output.SetText("(no output — repo may have no commits matching the filter)")
					return
				}
				output.SetText(text)
			})
		}(n, path)
	}
	nSelect.OnChanged = func(string) { fetch() }

	scope := "this repo"
	if path != "" {
		scope = filepath.Base(path)
	}

	header := container.NewHBox(
		widget.NewLabel("Showing patches for "+scope+" — last"),
		nSelect,
		widget.NewLabel("commit(s)"),
	)

	scroll := container.NewVScroll(output)
	scroll.SetMinSize(fyne.NewSize(820, 520))

	var d *dialog.CustomDialog
	closeBtn := widget.NewButton("Close", func() {
		if d != nil {
			d.Hide()
		}
	})
	footer := container.NewBorder(nil, nil, nil, closeBtn, widget.NewLabel(""))

	root := container.NewBorder(header, footer, nil, nil, scroll)

	title := "git log -p"
	if path != "" {
		title += " — " + filepath.Base(path)
	}
	d = dialog.NewCustomWithoutButtons(title, root, parent)
	d.Resize(fyne.NewSize(900, 680))
	d.Show()

	fetch()
}

// runGitLogPatch shells out to `git log -p -n N [-- <path>]` and
// returns the captured stdout. A short timeout protects against
// pathological inputs (e.g. N=100 on a repo whose history is dominated
// by huge binary diffs); 30s is comfortably longer than any reasonable
// patch dump but short enough to fail fast if something is wrong.
func runGitLogPatch(repoRoot, n, path string) (string, error) {
	if repoRoot == "" {
		return "", fmt.Errorf("no repo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"-C", repoRoot, "log", "-p", "-n", n}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git log -p -n %s timed out — try a smaller N", n)
		}
		return string(out), err
	}
	return string(out), nil
}
