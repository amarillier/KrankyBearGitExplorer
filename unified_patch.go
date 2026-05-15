package main

import (
	"io"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/pmezard/go-difflib/difflib"
)

// normalizePatchNewlines converts CRLF/CR to LF for consistent line-oriented diffs.
func normalizePatchNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func patchDisplayName(path, fallback string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return fallback
	}
	base := filepath.Base(path)
	if base == "" || base == "." {
		return fallback
	}
	return base
}

// patchDisplayNames returns distinct ---/+++ labels for the unified diff header.
func patchDisplayNames(leftPath, rightPath string) (from, to string) {
	from = patchDisplayName(leftPath, "left")
	to = patchDisplayName(rightPath, "right")
	if from == to {
		return "left", "right"
	}
	return from, to
}

// buildUnifiedPatch returns a unified diff from left (old) to right (new), suitable for patch(1).
func buildUnifiedPatch(leftPath, rightPath, leftText, rightText string) (string, error) {
	leftText = normalizePatchNewlines(leftText)
	rightText = normalizePatchNewlines(rightText)
	from, to := patchDisplayNames(leftPath, rightPath)
	ud := difflib.UnifiedDiff{
		A:        splitTextLines(leftText),
		FromFile: from,
		B:        splitTextLines(rightText),
		ToFile:   to,
		Context:  3,
		Eol:      "\n",
	}
	return difflib.GetUnifiedDiffString(ud)
}

func suggestedPatchFileName(leftPath, rightPath string) string {
	a, b := patchDisplayNames(leftPath, rightPath)
	sa := sanitizePatchFileStem(a)
	sb := sanitizePatchFileStem(b)
	if sa != "" && sb != "" {
		return sa + "-" + sb + ".patch"
	}
	return "kbgitexplorer-unified.patch"
}

func sanitizePatchFileStem(s string) string {
	var out strings.Builder
	for _, r := range s {
		if r < 32 || r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			continue
		}
		out.WriteRune(r)
	}
	return strings.TrimSpace(out.String())
}

func (v *diffView) exportUnifiedPatch() {
	if v.win == nil {
		return
	}
	if v.leftT == "" && v.rightT == "" && v.leftP == "" && v.rightP == "" {
		dialog.ShowInformation("Export unified patch", "Open or load two files first.", v.win)
		return
	}
	patch, err := buildUnifiedPatch(v.leftP, v.rightP, v.leftT, v.rightT)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	name := suggestedPatchFileName(v.leftP, v.rightP)
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, v.win) })
			return
		}
		if w == nil {
			return
		}
		defer w.Close()
		_, err = io.WriteString(w, patch)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, v.win) })
		}
	}, v.win)
	d.SetFileName(name)
	d.Show()
}

func (v *diffView) copyUnifiedPatchToClipboard() {
	if v.win == nil {
		return
	}
	if v.leftT == "" && v.rightT == "" && v.leftP == "" && v.rightP == "" {
		dialog.ShowInformation("Copy unified patch", "Open or load two files first.", v.win)
		return
	}
	patch, err := buildUnifiedPatch(v.leftP, v.rightP, v.leftT, v.rightT)
	if err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	v.copyToClipboard(patch)
}
