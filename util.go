package main

import (
	"errors"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	updatechecker "github.com/amarillier/go-update-checker"
)

func checkFileExists(filePath string) bool {
	_, error := os.Stat(filePath)
	return !errors.Is(error, os.ErrNotExist)
}

func updateChecker(repoOwner string, repo string, repoName string, repodl string, checkStatePath string) (string, bool) {
	if checkStatePath != "" {
		updatechecker.SetCheckStatePath(checkStatePath)
	}
	uc := updatechecker.New(repoOwner, repo, repoName, repodl, 0, false)
	uc.CheckForUpdate(appVersion)
	updtmsg := uc.Message
	return updtmsg, uc.UpdateAvailable
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
// scrollDialogContent wraps dialog content in a vertical scroll so it stays
// reachable when the dialog is clamped to a small main window. The scroll's
// min size follows the content up to maxH, so roomy windows look unchanged.
func scrollDialogContent(content fyne.CanvasObject, maxH float32) *container.Scroll {
	s := container.NewVScroll(content)
	min := content.MinSize()
	if min.Height > maxH {
		min.Height = maxH
	}
	s.SetMinSize(fyne.NewSize(min.Width, min.Height))
	return s
}
