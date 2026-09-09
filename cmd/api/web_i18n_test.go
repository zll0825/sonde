package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// exemptFromCJKGuard lists the files this guard cannot judge.
//
//   - i18n.js and landing.js are dictionaries: their Chinese IS the zh half of
//     a zh/en pair, not a hardcoded string. landing.js is a standalone page and
//     carries its own LANDING_I18N / PIPELINE_DETAILS.
var exemptFromCJKGuard = map[string]bool{
	"i18n.js":    true,
	"landing.js": true,
}

// TestFrontendJSHasNoHardcodedCJK locks the i18n boundary: every user-visible
// string must come from TRANSLATIONS in i18n.js via t(), never be concatenated
// inline. The plugins tab shipped "13 条" / "0 次" straight into the DOM, which
// stayed invisible in Chinese and only broke the English UI — nothing in the
// suite could catch it, because the Go tests only read app.html.
//
// Comments are stripped first: Chinese comments are normal in this codebase and
// carry no user-visible text.
func TestFrontendJSHasNoHardcodedCJK(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	srcDir := filepath.Join(filepath.Dir(thisFile), "../../web/src")

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		if exemptFromCJKGuard[e.Name()] {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(stripJSComments(string(raw)), "\n") {
			if col := strings.IndexFunc(line, isCJK); col >= 0 {
				t.Errorf("web/src/%s:%d has hardcoded CJK outside i18n.js: %q\n"+
					"\tmove the text into TRANSLATIONS and render it with t('<key>', {...})",
					e.Name(), i+1, strings.TrimSpace(line))
			}
		}
	}
}

// stripJSComments blanks out // and /* */ comments while preserving line count.
// It is deliberately naive — a "//" inside a string literal truncates the rest
// of that line, which can only ever hide a violation, never invent one.
func stripJSComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))

	inBlock := false
	for _, line := range strings.Split(src, "\n") {
		out := line
		if inBlock {
			if idx := strings.Index(out, "*/"); idx >= 0 {
				out, inBlock = out[idx+2:], false
			} else {
				out = ""
			}
		}
		if !inBlock {
			if idx := strings.Index(out, "/*"); idx >= 0 {
				if end := strings.Index(out[idx:], "*/"); end >= 0 {
					out = out[:idx] + out[idx+end+2:]
				} else {
					out, inBlock = out[:idx], true
				}
			}
			if idx := strings.Index(out, "//"); idx >= 0 {
				out = out[:idx]
			}
		}
		b.WriteString(out)
		b.WriteString("\n")
	}
	return b.String()
}

func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK punctuation （、。「」）
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Fullwidth forms （！？：）
		return true
	}
	return false
}
