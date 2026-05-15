package main

import (
	"errors"
	"os"

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