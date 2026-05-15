package main

import (
	"fmt"
	"os"

	"fyne.io/fyne/v2/dialog"
)

func writeTextFile(path, content string) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(path, []byte(content), mode)
}

// saveSideAttempt writes one pane’s buffer to its path when dirty. No-op if no path or not dirty.
func (v *diffView) saveSideAttempt(side int) {
	if v.win == nil {
		return
	}
	if (side == 0 && v.leftReadOnly) || (side == 1 && v.rightReadOnly) {
		return
	}
	var path, text string
	var dirty *bool
	if side == 0 {
		path, text, dirty = v.leftP, v.leftT, &v.leftDirty
	} else {
		path, text, dirty = v.rightP, v.rightT, &v.rightDirty
	}
	if path == "" || !*dirty {
		return
	}
	if err := writeTextFile(path, text); err != nil {
		dialog.ShowError(fmt.Errorf("save file: %w", err), v.win)
		return
	}
	*dirty = false
	v.refreshMainToolbar()
	v.refreshMainMenu()
}

func (v *diffView) saveBothAttempt() {
	v.saveSideAttempt(0)
	v.saveSideAttempt(1)
}
