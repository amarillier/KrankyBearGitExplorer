// Package main provides update checking dialog
package main

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var updateWindow fyne.Window

func showUpdateDialog(a fyne.App, message string, updateAvailable, localAhead bool) {
	if updateWindow != nil && updateWindow.Content().Visible() {
		windowShow(updateWindow)
		updateWindow.RequestFocus()
		return
	}

	branding := resourceKrankyBearNerdPng
	if localAhead {
		branding = resourceKrankyBearHackerPng
	}

	updateWindow = a.NewWindow(appName + " - Update Check")
	updateWindow.SetIcon(branding)

	icon := newBrandingDialogImage(branding)

	messageLabel := widget.NewLabel(message)
	// Wrapped labels report huge MinSize before layout; window Resize is Max'd with it.
	messageLabel.Wrapping = fyne.TextWrapOff
	messageLabel.Alignment = fyne.TextAlignLeading

	var textColumn *fyne.Container
	if updateAvailable {
		releaseURL, _ := url.Parse("https://github.com/amarillier/KrankyBearGitExplorer/releases/latest")
		releaseLink := widget.NewHyperlink("Download Latest Release", releaseURL)
		releaseLink.Alignment = fyne.TextAlignLeading

		notesURL, _ := url.Parse("https://github.com/amarillier/KrankyBearGitExplorer/blob/main/ReleaseNotes.txt")
		notesLink := widget.NewHyperlink("View Release Notes", notesURL)
		notesLink.Alignment = fyne.TextAlignLeading

		textColumn = container.NewVBox(
			messageLabel,
			widget.NewSeparator(),
			releaseLink,
			notesLink,
		)
	} else {
		textColumn = container.NewVBox(messageLabel)
	}

	// Border gives the text column the remaining width for display; MinSize for
	// wrapped labels is still wrong before layout, so the label uses TextWrapOff.
	mainArea := container.NewBorder(
		nil, nil,
		container.NewPadded(icon), nil,
		container.NewPadded(textColumn),
	)

	updateWindow.SetContent(container.NewPadded(mainArea))
	updateWindow.Resize(fyne.NewSize(640, 320))

	updateWindow.SetCloseIntercept(func() {
		windowHide(updateWindow)
	})

	windowShow(updateWindow)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
