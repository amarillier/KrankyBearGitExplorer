package main

import (
	_ "embed"
	"fmt"
	"strings"
	"time"
)

// The version lives in the VERSION file (build/version.sh X.Y.Z).
//
//go:embed VERSION
var versionFile string

var appVersion = strings.TrimSpace(versionFile)

const (
	appAuthor = "Allan Marillier"
)

var appName = "KrankyBear GitExplorer"
var appCopyright = buildCopyrightNotice()

func buildCopyrightNotice() string {
	const startYear = 2026
	currentYear := time.Now().Year()
	if currentYear <= startYear {
		return "Copyright (c) Allan Marillier, 2026"
	}
	return fmt.Sprintf("Copyright (c) Allan Marillier, 2026-%d", currentYear)
}

func main() {
	runExplorerApp()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
