package main

import (
	"net/url"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"kbgitexplorer/internal/startup"
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
	icon := newBrandingDialogImage(resourceKrankyBearNerdPng)
	var iconDisplay fyne.CanvasObject = icon
	if appIsAheadOfLatestRelease() {
		// icon = newBrandingDialogImage(resourceKrankyBearHackerPng)
		badge := newBrandingBadgeImage(resourceKrankyBearHardHatPng)
		iconDisplay = container.NewHBox(icon, badge)
	}

	title := widget.NewLabelWithStyle(appName, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	version := widget.NewLabel("Version: " + appVersion)
	version.Alignment = fyne.TextAlignCenter

	description := widget.NewLabel("A comprehensive Git repository explorer and management utility")
	description.Alignment = fyne.TextAlignCenter
	description.Wrapping = fyne.TextWrapWord

	copyright := widget.NewLabel(appCopyright)
	copyright.Alignment = fyne.TextAlignCenter
	author := widget.NewLabel("By " + appAuthor)
	author.Alignment = fyne.TextAlignCenter

	licenseURL, _ := url.Parse("https://github.com/amarillier/KrankyBearGitExplorer/blob/allanm/LICENSE")
	licenseLink := widget.NewHyperlink("License Information", licenseURL)
	licenseLink.Alignment = fyne.TextAlignCenter

	githubURL, _ := url.Parse("https://github.com/amarillier/KrankyBearGitExplorer")
	githubLink := widget.NewHyperlink("GitHub Repository", githubURL)
	githubLink.Alignment = fyne.TextAlignCenter

	// Windows only: which OpenGL path this launch uses (hardware, or the Mesa3D software fallback and its rasterizer).
	var renderingRow fyne.CanvasObject = layout.NewSpacer()
	if runtime.GOOS == "windows" {
		credit := widget.NewLabel("The Windows package includes Mesa 3D (MIT and other licences) as a software OpenGL fallback: see mesa-fallback\\MESA-NOTICE.txt.")
		credit.Wrapping = fyne.TextWrapWord
		credit.Alignment = fyne.TextAlignCenter
		renderingRow = container.NewVBox(container.NewCenter(widget.NewLabel(renderingStatusText())), credit)
	}

	textColumn := container.NewVBox(
		title,
		version,
		description,
		widget.NewSeparator(),
		copyright,
		author,
		renderingRow,
		widget.NewSeparator(),
		container.NewCenter(licenseLink),
		container.NewCenter(githubLink),
	)

	mainArea := container.NewHBox(
		container.NewPadded(iconDisplay),
		container.NewPadded(textColumn),
	)

	aboutWindow.SetContent(container.NewPadded(mainArea))
	aboutWindow.Resize(fyne.NewSize(520, 480))

	aboutWindow.SetCloseIntercept(func() {
		aboutWindow.Hide()
	})

	aboutWindow.Show()
}

// renderingStatusText reports which OpenGL path this launch uses: real hardware, or the bundled Mesa3D software fallback with
// the rasterizer (llvmpipe fast / softpipe safe) and the pinned Mesa version when known.
func renderingStatusText() string {
	active, version, driver := startup.MesaFallbackActive()
	if !active {
		return "Rendering: hardware OpenGL"
	}
	if version == "" {
		return "Rendering: Mesa3D software OpenGL (" + driver + ")"
	}
	return "Rendering: Mesa3D software OpenGL (" + driver + ", Mesa " + version + ")"
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
