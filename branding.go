package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// Dialog branding matches my standards about.go / help.go (150×150).
const brandingImageSizeDialog = 150

// Main window header matches my standards mainwindow header krankybear (80×80).
const brandingImageSizeHeader = 80

// "Ahead of latest release" badge shown beside -- not instead of -- the main
// dialog icon (see update.go's showUpdateDialog and about.go's showAbout):
// big enough to read clearly at roughly half the dialog icon's size, small
// enough to stay a highlight rather than compete with it.
const brandingImageSizeBadge = 75

func newBrandingDialogImage(res fyne.Resource) *canvas.Image {
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(brandingImageSizeDialog, brandingImageSizeDialog))
	return img
}

func newBrandingHeaderImage(res fyne.Resource) *canvas.Image {
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(brandingImageSizeHeader, brandingImageSizeHeader))
	return img
}

func newBrandingBadgeImage(res fyne.Resource) *canvas.Image {
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(brandingImageSizeBadge, brandingImageSizeBadge))
	return img
}
