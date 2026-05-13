package main

import "strings"

// Visible whitespace (similar to kdiff / many merge tools):
//   - each space → middle dot (·)
//   - each tab → right arrow (→), one glyph per tab character in the source
//
// When showWhitespace is false, tabs are expanded to four spaces for a stable column layout.
func formatLineForDisplay(raw string, showWhitespace bool) string {
	if !showWhitespace {
		return strings.ReplaceAll(raw, "\t", "    ")
	}
	var b strings.Builder
	b.Grow(len(raw) + 8)
	for _, r := range raw {
		switch r {
		case ' ':
			b.WriteRune('·')
		case '\t':
			b.WriteRune('→')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
