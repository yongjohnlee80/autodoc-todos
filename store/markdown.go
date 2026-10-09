package store

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const managedMarker = "<!-- ─── auto-core.todo schema v1\nLifecycle fields are managed. Edit title and Markdown body freely.\nStandalone edits do not emit events or execute automation.\n─── -->"

func splitMarkdown(data []byte) (fm, body []byte, start, end int, err error) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSuffix(lines[0], []byte("\r\n")), []byte("---")) && !bytes.Equal(lines[0], []byte("---\n")) {
		return nil, nil, 0, 0, errors.New("missing opening --- frontmatter delimiter")
	}
	start = len(lines[0])
	pos := start
	for _, line := range lines[1:] {
		if string(bytes.TrimRight(line, "\r\n")) == "---" {
			return data[start:pos], data[pos+len(line):], start, pos, nil
		}
		pos += len(line)
	}
	return nil, nil, 0, 0, errors.New("missing closing --- frontmatter delimiter")
}

var markerPattern = regexp.MustCompile(`(?s)<!-- ─── auto-core\.todo schema v1.*?─── -->\s*\n?`)
var headingPattern = regexp.MustCompile(`^\s*#\s+[^\n]*\n?`)

func cleanBody(body string) string {
	if loc := markerPattern.FindStringIndex(body); loc != nil {
		body = body[:loc[0]] + body[loc[1]:]
	}
	body = headingPattern.ReplaceAllString(body, "")
	return strings.Trim(body, "\n")
}

// Only changed single-line lifecycle scalars are patched. Unchanged YAML and
// the complete Markdown body (including comments and line endings) stay byte-exact.
func (t *Task) patchMarkdown() ([]byte, error) {
	fm, _, start, end, err := splitMarkdown(t.Original)
	if err != nil {
		return nil, err
	}
	before, err := Parse(t.Original)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(fm), "\n")
	eol := "\n"
	if bytes.Contains(fm, []byte("\r\n")) {
		eol = "\r\n"
	}
	var added strings.Builder
	for _, key := range []string{"status", "status_changed", "completed_at", "archived_at"} {
		old, next := field(&before.Document, key), field(&t.Document, key)
		if old != nil && next != nil && old.Value == next.Value && old.Tag == next.Tag {
			continue
		}
		if old == nil {
			if next != nil {
				fmt.Fprintf(&added, "%s: %s%s", key, next.Value, eol)
			}
			continue
		}
		if old.Kind != yaml.ScalarNode || old.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || old.Line < 1 || old.Line > len(lines) {
			return nil, fmt.Errorf("%s is not a patchable single-line scalar", key)
		}
		line := lines[old.Line-1]
		col := old.Column - 1
		if col < 0 || col >= len(line) || old.Anchor != "" || strings.Contains(old.Value, "\n") {
			return nil, fmt.Errorf("%s has an unsafe scalar representation", key)
		}
		// Locate the end of the scalar without treating a quoted # as a comment.
		valueEnd := col
		quote := byte(0)
		for valueEnd < len(line) {
			c := line[valueEnd]
			if quote != 0 {
				if c == '\\' && quote == '"' {
					valueEnd += 2
					continue
				}
				if c == quote {
					if quote == '\'' && valueEnd+1 < len(line) && line[valueEnd+1] == '\'' {
						valueEnd += 2
						continue
					}
					quote = 0
				}
			} else {
				if c == '\'' || c == '"' {
					quote = c
				}
				if c == '#' && valueEnd > 0 && (line[valueEnd-1] == ' ' || line[valueEnd-1] == '\t') || c == '\r' || c == '\n' {
					break
				}
			}
			valueEnd++
		}
		for valueEnd > col && (line[valueEnd-1] == ' ' || line[valueEnd-1] == '\t') {
			valueEnd--
		}
		if next == nil {
			// Keep inline comments even when their lifecycle field is removed.
			suffix := line[valueEnd:]
			if strings.Contains(suffix, "#") {
				lines[old.Line-1] = strings.TrimLeft(suffix, " \t")
			} else {
				lines[old.Line-1] = ""
			}
		} else {
			lines[old.Line-1] = line[:col] + next.Value + line[valueEnd:]
		}
	}
	patched := strings.Join(lines, "")
	return []byte(string(t.Original[:start]) + patched + added.String() + string(t.Original[end:])), nil
}
