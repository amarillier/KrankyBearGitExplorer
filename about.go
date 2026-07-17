package main

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var aboutWindow fyne.Window

// showAbout displays the About dialog with app branding, version, and links
func showAbout(a fyne.App) {
	if aboutWindow != nil && aboutWindow.Content().Visible() {
		aboutWindow.Show()
		aboutWindow.RequestFocus()
		return
	}

	aboutWindow = a.NewWindow(appName + " - About")
	aboutWindow.SetIcon(resourceKrankyBearHackerPng)

	// A HardHat badge appears beside the normal app icon when this build is
	// ahead of the latest published release (an unpublished/dev build), read
	// from the update-checker's on-disk cache — see appIsAheadOfLatestRelease
	// in versioncheck.go for why this is cache-only rather than a live check.
	icon := newBrandingDialogImage(resourceKrankyBearHackerPng)
	var iconDisplay fyne.CanvasObject = icon
	if appIsAheadOfLatestRelease() {
		badge := newBrandingBadgeImage(resourceKrankyBearHardHatPng)
		iconDisplay = container.NewHBox(icon, badge)
	}

	title := widget.NewLabelWithStyle(appName, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	version := widget.NewLabel("Version: " + appVersion)
	version.Alignment = fyne.TextAlignCenter

	description := widget.NewLabel("A comprehensive Diff management utility")
	description.Alignment = fyne.TextAlignCenter
	description.Wrapping = fyne.TextWrapWord

	copyright := widget.NewLabel(appCopyright)
	copyright.Alignment = fyne.TextAlignCenter
	author := widget.NewLabel("By " + appAuthor)
	author.Alignment = fyne.TextAlignCenter

	licenseURL, _ := url.Parse("https://github.com/amarillier/KrankyBearDiff/blob/allanm/LICENSE")
	licenseLink := widget.NewHyperlink("License Information", licenseURL)
	licenseLink.Alignment = fyne.TextAlignCenter

	githubURL, _ := url.Parse("https://github.com/amarillier/KrankyBearDiff")
	githubLink := widget.NewHyperlink("GitHub Repository", githubURL)
	githubLink.Alignment = fyne.TextAlignCenter

	textColumn := container.NewVBox(
		title,
		version,
		description,
		widget.NewSeparator(),
		copyright,
		author,
		widget.NewSeparator(),
		container.NewCenter(licenseLink),
		container.NewCenter(githubLink),
	)

	mainArea := container.NewHBox(
		container.NewPadded(iconDisplay),
		container.NewPadded(textColumn),
	)

	aboutWindow.SetContent(container.NewPadded(mainArea))
	aboutWindow.Resize(fyne.NewSize(520, 420))

	aboutWindow.SetCloseIntercept(func() {
		aboutWindow.Hide()
	})

	aboutWindow.Show()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
