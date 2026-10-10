package lsp

import (
	"unicode"
	"unicode/utf8"
)

// tokenTypes is the semantic-token legend, indexes must match semanticTokens.
var tokenTypes = []string{"keyword", "comment", "string", "number", "operator", "variable"}

const (
	tokKeyword = 0
	tokComment = 1
	tokString  = 2
	tokNumber  = 3
	tokOper    = 4
	tokVar     = 5
)

var keywords = map[string]bool{
	"break": true, "default": true, "func": true, "interface": true, "select": true,
	"case": true, "defer": true, "go": true, "map": true, "struct": true,
	"chan": true, "else": true, "goto": true, "package": true, "switch": true,
	"const": true, "fallthrough": true, "if": true, "range": true, "type": true,
	"continue": true, "for": true, "import": true, "return": true, "var": true,
}

// semanticTokens returns LSP delta-encoded tokens for src.
func semanticTokens(src string) []int {
	type tok struct{ line, col, len, typ int }
	var toks []tok
	line, col := 0, 0
	i := 0
	for i < len(src) {
		r, w := utf8.DecodeRuneInString(src[i:])
		switch {
		case r == '/' && i+1 < len(src) && src[i+1] == '/':
			start := i
			for i < len(src) && src[i] != '\n' {
				i++
			}
			toks = append(toks, tok{line, col, utf16Len(src[start:i]), tokComment})
			col += utf16Len(src[start:i])
		case r == '/' && i+1 < len(src) && src[i+1] == '*':
			startLine, startCol := line, col
			start := i
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					line++
					col = 0
				} else {
					col++
				}
				i++
			}
			if i+1 < len(src) {
				i += 2
			}
			// Report only the first line of a block comment; the rest is still skipped.
			toks = append(toks, tok{startLine, startCol, utf16Len(firstLine(src[start:])), tokComment})
			_ = start
		case r == '"' || r == '\'' || r == '`':
			start := i
			quote := src[i]
			i++
			if quote == '`' {
				for i < len(src) && src[i] != '`' {
					i++
				}
			} else {
				for i < len(src) && src[i] != quote && src[i] != '\n' {
					if src[i] == '\\' && i+1 < len(src) {
						i += 2
						continue
					}
					i++
				}
			}
			if i < len(src) && src[i] == quote {
				i++
			}
			text := src[start:i]
			toks = append(toks, tok{line, col, utf16Len(text), tokString})
			for _, c := range text {
				if c == '\n' {
					line++
					col = 0
				}
			}
			col += utf16Len(lastLine(text))
		case r == '\n':
			line++
			col = 0
			i += w
		case unicode.IsSpace(r):
			col += utf16Len(src[i : i+w])
			i += w
		case unicode.IsLetter(r) || r == '_':
			start := i
			i += w
			for i < len(src) {
				rr, ww := utf8.DecodeRuneInString(src[i:])
				if !unicode.IsLetter(rr) && !unicode.IsDigit(rr) && rr != '_' {
					break
				}
				i += ww
			}
			word := src[start:i]
			kind := tokVar
			if keywords[word] {
				kind = tokKeyword
			}
			toks = append(toks, tok{line, col, utf16Len(word), kind})
			col += utf16Len(word)
		case unicode.IsDigit(r):
			start := i
			for i < len(src) && (src[i] == '.' || src[i] == '_' || (src[i] >= '0' && src[i] <= '9') || (src[i] >= 'a' && src[i] <= 'f') || (src[i] >= 'A' && src[i] <= 'F') || src[i] == 'x' || src[i] == 'X' || src[i] == 'o' || src[i] == 'O' || src[i] == 'b' || src[i] == 'B') {
				i++
			}
			text := src[start:i]
			toks = append(toks, tok{line, col, utf16Len(text), tokNumber})
			col += utf16Len(text)
		default:
			if stringsOp(r) {
				toks = append(toks, tok{line, col, 1, tokOper})
			}
			col += utf16Len(src[i : i+w])
			i += w
		}
	}
	var data []int
	pl, pc := 0, 0
	for _, t := range toks {
		dl := t.line - pl
		dc := t.col
		if dl == 0 {
			dc = t.col - pc
		}
		data = append(data, dl, dc, t.len, t.typ, 0)
		pl, pc = t.line, t.col
	}
	if data == nil {
		return []int{}
	}
	return data
}

func stringsOp(r rune) bool {
	switch r {
	case '+', '-', '*', '/', '%', '=', '<', '>', '!', '&', '|', '^', ':', '.', ',', ';', '(', ')', '[', ']', '{', '}':
		return true
	default:
		return false
	}
}

func firstLine(s string) string {
	if i := indexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLine(s string) string {
	if i := lastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
