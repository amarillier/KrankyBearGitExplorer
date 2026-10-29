package main

import (
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var releaseNotesWindow fyne.Window

// releaseNotesFileNames are tried in order, both beside the running
// executable (installed/packaged layout) and in the current working
// directory (running via `go run .` from the repo root during
// development). .md is preferred — real headers/bold/lists via
// widget.NewRichTextFromMarkdown — but this project's older plain-text
// entries (still present at the top of ReleaseNotes.md after the
// .txt→.md rename) render fine as plain paragraphs too, so one file
// covers both eras. Both PascalCase and all-lowercase are checked since
// Linux is case-sensitive.
var releaseNotesFileNames = []string{
	"ReleaseNotes.md", "releasenotes.md",
	"ReleaseNotes.txt", "releasenotes.txt",
}

// showReleaseNotes displays this app's own bundled release notes in a
// dedicated window, matching showHelp's pattern (singleton, scrollable,
// Hide not Close). Reads from disk rather than embedding at compile
// time so the file can be updated (e.g. via the installer) without a
// rebuild. The read + markdown parse happens in a background goroutine
// so the window appears immediately with a loading indicator rather
// than blocking the caller (Help-menu click, or the tray goroutine) —
// same reasoning as showReleaseNotes in KrankyBearInstallerBear.
func showReleaseNotes(a fyne.App) {
	if releaseNotesWindow != nil && releaseNotesWindow.Content().Visible() {
		windowShow(releaseNotesWindow)
		releaseNotesWindow.RequestFocus()
		return
	}

	releaseNotesWindow = a.NewWindow(appName + " - Release Notes")
	releaseNotesWindow.SetIcon(resourceKrankyBearNerdPng)

	icon := newBrandingDialogImage(resourceKrankyBearNerdPng)

	loadingBar := widget.NewProgressBarInfinite()
	loading := container.NewVBox(
		widget.NewLabel("Loading release notes..."),
		loadingBar,
	)

	scroll := container.NewScroll(loading)
	scroll.SetMinSize(fyne.NewSize(700, 550))

	header := container.NewVBox(
		widget.NewLabelWithStyle(appName+" — Release Notes", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
	)

	mainArea := container.NewHBox(container.NewPadded(icon), scroll)

	releaseNotesWindow.SetContent(container.NewPadded(container.NewBorder(header, nil, nil, nil, mainArea)))
	releaseNotesWindow.Resize(fyne.NewSize(820, 650))
	releaseNotesWindow.SetCloseIntercept(func() { windowHide(releaseNotesWindow) })
	windowShow(releaseNotesWindow)

	go func() {
		body := releaseNotesBody()
		richText := widget.NewRichTextFromMarkdown(body)
		richText.Wrapping = fyne.TextWrapWord

		fyne.Do(func() {
			loadingBar.Stop()
			scroll.Content = richText
			scroll.Refresh()
			releaseNotesWindow.Canvas().Refresh(scroll)
		})
	}()
}

// releaseNotesBody reads the first of releaseNotesFileNames found beside
// the running executable or in the current working directory, or
// explains clearly why none could be found — a standalone binary copied
// without its accompanying ReleaseNotes file is a real scenario, so this
// reports it plainly rather than showing an empty window. Returned as
// Markdown either way, since the caller always renders through
// widget.NewRichTextFromMarkdown.
func releaseNotesBody() string {
	var dirs []string
	if exePath, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exePath))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	for _, dir := range dirs {
		for _, name := range releaseNotesFileNames {
			if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				return string(data)
			}
		}
	}
	return "**Release Notes could not be found.**\n\n" +
		"This usually means you're running a standalone copy of " + appName + " without " +
		"the ReleaseNotes file that normally travels alongside it. Check the project's release history here:\n\n" +
		"[" + appName + " on GitHub](https://github.com/amarillier/KrankyBearGitExplorer)"
}
