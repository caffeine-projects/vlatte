package config

import (
	"bytes"
	"strings"
)

// Documents splits data, in a format Format names, into its YAML documents, which --- starts and ... ends. Each keeps
// the line numbers it has in data, so a parse error points to the line of the whole. A document with no content, such
// as one before the first --- or after the last, is left out. JSON, and YAML with no content, give data whole.
func Documents(data []byte, format string) [][]byte {
	if f, _ := Format(format); f == "json" {
		return [][]byte{data}
	}

	var (
		docs    [][]byte
		start   int  // offset of the document being read
		line    int  // lines before the current one
		first   int  // lines before the document being read
		content bool // whether the document being read has content
		marked  bool // whether the document being read started with ---
		offset  int  // offset of the current line
	)

	cut := func(end, next int) {
		if content {
			doc := append(bytes.Repeat([]byte("\n"), first), data[start:end]...)
			docs = append(docs, doc)
		}

		start, first, content, marked = end, next, false, false
	}

	// A byte order mark stays in the first document, but is no content.
	offset = len(data) - len(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))

	for l := range bytes.Lines(data[offset:]) {
		switch {
		// A --- starts a document, and ends the one before when that one has content or a --- of its own: the
		// comments and directives before the first --- belong to the first document.
		case marker(l, "---"):
			if content || marked {
				cut(offset, line)
			}

			content, marked = !isBlank(l[3:]), true
		case marker(l, "..."):
			cut(offset+len(l), line+1)
		case !isBlank(l) && l[0] != '%':
			content = true
		}

		offset += len(l)
		line++
	}

	cut(len(data), line)

	if len(docs) == 0 {
		return [][]byte{data}
	}

	return docs
}

// marker reports whether line starts with the document marker m, --- or ..., followed by a space or its end.
func marker(line []byte, m string) bool {
	rest, ok := bytes.CutPrefix(line, []byte(m))
	return ok && (len(rest) == 0 || strings.ContainsRune(" \t\r\n", rune(rest[0])))
}

// isBlank reports whether line holds nothing but spaces and a comment.
func isBlank(line []byte) bool {
	t := bytes.TrimSpace(line)
	return len(t) == 0 || t[0] == '#'
}
