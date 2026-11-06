package lsp

import (
	"fmt"
	"strings"
)

// UTF16Len returns the number of UTF-16 code units needed to encode s. LSP
// positions count these units: a character outside the BMP (an emoji, some CJK
// extensions) occupies two.
func UTF16Len(s string) int {
	units := 0
	for _, r := range s {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}

// ValidatePosition checks a one-based line and UTF-16 column against text and
// returns the zero-based LSP position to send. Out-of-range coordinates are
// rejected with a stable, readable message instead of being forwarded to the
// server.
func ValidatePosition(text string, line, column int) (Position, error) {
	if line < 1 {
		return Position{}, fmt.Errorf("line %d is invalid: lines are 1-based", line)
	}
	if column < 1 {
		return Position{}, fmt.Errorf("column %d is invalid: columns are 1-based UTF-16 code units", column)
	}
	lines := splitLines(text)
	if line > len(lines) {
		return Position{}, fmt.Errorf("line %d is outside the file (it has %d line(s))", line, len(lines))
	}
	width := UTF16Len(lines[line-1])
	if column > width+1 {
		return Position{}, fmt.Errorf("column %d is outside line %d (it has %d UTF-16 code unit(s))", column, line, width)
	}
	return Position{Line: line - 1, Character: column - 1}, nil
}

// splitLines splits text the way LSP documents count lines (\r\n, \r and \n
// all end a line). A trailing newline yields a final empty line.
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}
