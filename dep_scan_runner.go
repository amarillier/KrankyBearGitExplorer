package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// depScanCommand builds the *exec.Cmd that will run dep-scan against repoRoot.
// Script resolution order:
//   1. <repoRoot>/dep-scan.{sh,ps1}  — project-local copy (vendored by the
//      repo being explored; teammates get it out-of-the-box on clone).
//   2. ~/.claude/skills/dep-scan/dep-scan.{sh,ps1}  — user-level install
//      (the same skill we built earlier — the canonical home).
// Returns a descriptive error if neither exists.
func depScanCommand(repoRoot string) (*exec.Cmd, string, error) {
	scriptName := "dep-scan.sh"
	if runtime.GOOS == "windows" {
		scriptName = "dep-scan.ps1"
	}

	candidates := make([]string, 0, 2)
	if repoRoot != "" {
		candidates = append(candidates, filepath.Join(repoRoot, scriptName))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".claude", "skills", "dep-scan", scriptName))
	}

	var scriptPath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			scriptPath = c
			break
		}
	}
	if scriptPath == "" {
		return nil, "", fmt.Errorf(
			"dep-scan script not found.\n\nLooked for %s in:\n  • %s\n\nInstall the dep-scan skill at ~/.claude/skills/dep-scan/ or drop %s into the project root. See README_DEPSCAN.md for install instructions.",
			scriptName, joinPathsBullet(candidates), scriptName,
		)
	}

	if runtime.GOOS == "windows" {
		return exec.Command("pwsh", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath, repoRoot), scriptPath, nil
	}
	return exec.Command("bash", scriptPath, repoRoot), scriptPath, nil
}

func joinPathsBullet(paths []string) string {
	out := ""
	for i, p := range paths {
		if i > 0 {
			out += "\n  • "
		}
		out += p
	}
	return out
}

// runDepScanForRepo orchestrates: build the command, show a non-cancelable
// progress dialog (the scan typically runs 5-60 seconds depending on repo
// size and ecosystem coverage), run on a goroutine, then replace the
// progress dialog with a result dialog rendering the markdown report.
func runDepScanForRepo(a fyne.App, parent fyne.Window, repoRoot string) {
	cmd, scriptPath, err := depScanCommand(repoRoot)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}

	// Progress dialog: an indeterminate bar with a couple of explanatory lines.
	progBar := widget.NewProgressBarInfinite()
	progLbl := widget.NewLabel(fmt.Sprintf("Running dep-scan against:\n  %s\n\nUsing script: %s\n\nThis usually takes a few seconds to a minute depending on the number of manifests in the repo.", repoRoot, scriptPath))
	progLbl.Wrapping = fyne.TextWrapWord
	progContent := container.NewVBox(progLbl, progBar)
	progDlg := dialog.NewCustom("Dep-Scan", "Hide (continues in background)", progContent, parent)
	progDlg.Resize(fyne.NewSize(560, 220))
	progDlg.Show()

	started := time.Now()
	go func() {
		out, runErr := cmd.CombinedOutput()
		elapsed := time.Since(started).Round(time.Millisecond)
		fyne.Do(func() {
			progDlg.Hide()
			showDepScanResult(a, parent, repoRoot, scriptPath, elapsed, string(out), runErr)
		})
	}()
}

// showDepScanResult presents the captured markdown report (or the error from
// a missing-tool/script-failure case) in a scrollable dialog with a "Copy
// report to clipboard" button.
func showDepScanResult(a fyne.App, parent fyne.Window, repoRoot, scriptPath string, elapsed time.Duration, output string, runErr error) {
	if runErr != nil {
		// If the script ran but produced output even on error, show the
		// output so the user sees the tool-missing diagnostic. Otherwise
		// surface the raw error.
		if output != "" {
			showDepScanReportDialog(a, parent, repoRoot, scriptPath, elapsed, output, runErr)
			return
		}
		dialog.ShowError(fmt.Errorf("dep-scan failed: %w", runErr), parent)
		return
	}
	showDepScanReportDialog(a, parent, repoRoot, scriptPath, elapsed, output, nil)
}

func showDepScanReportDialog(a fyne.App, parent fyne.Window, repoRoot, scriptPath string, elapsed time.Duration, report string, runErr error) {
	// Render the markdown report via widget.RichText so headings, lists,
	// and inline code styling all come through.
	rt := widget.NewRichTextFromMarkdown(report)
	rt.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(rt)
	scroll.SetMinSize(fyne.NewSize(820, 480))

	headerLines := []string{
		fmt.Sprintf("Repo: %s", repoRoot),
		fmt.Sprintf("Script: %s", scriptPath),
		fmt.Sprintf("Elapsed: %s", elapsed),
	}
	if runErr != nil {
		// Tool reported a non-zero exit but produced output — usually a
		// "tool not found" or "vulns found" diagnostic. Flag it.
		headerLines = append(headerLines, fmt.Sprintf("Exit status: %v", runErr))
	}
	hdr := widget.NewLabel(""+headerLines[0]+"\n"+headerLines[1]+"\n"+headerLines[2]+func() string {
		if len(headerLines) > 3 {
			return "\n" + headerLines[3]
		}
		return ""
	}())
	hdr.TextStyle = fyne.TextStyle{Monospace: true}

	copyBtn := widget.NewButton("Copy report to clipboard", func() {
		if c := fyne.CurrentApp().Clipboard(); c != nil {
			c.SetContent(report)
		}
	})
	footer := container.NewHBox(layout.NewSpacer(), copyBtn)

	content := container.NewBorder(
		container.NewVBox(hdr, widget.NewSeparator()),
		footer,
		nil, nil,
		scroll,
	)

	title := "Dep-Scan Report — " + filepath.Base(repoRoot)
	d := dialog.NewCustom(title, "Close", content, parent)
	d.Resize(fyne.NewSize(900, 640))
	d.Show()
}
