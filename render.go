package main

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/yongjohnlee80/autodoc-todos/store"
	"github.com/yongjohnlee80/autodoc/plugin"
)

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func clipped(s string, width int) string {
	r := []rune(clean(s))
	if len(r) <= max(0, width) {
		return string(r)
	}
	if width <= 1 {
		return strings.Repeat(".", max(0, width))
	}
	return string(r[:width-1]) + "…"
}

func (t *todos) draw() {
	if t.send != nil {
		_ = t.send(t.render())
	}
}

func (t *todos) render() *plugin.Frame {
	f := plugin.NewFrame(t.w, t.h)
	accent := plugin.Style{FG: t.theme.Colors["document.cursor"], Bold: true}
	if accent.FG == "" {
		accent.FG = "cyan"
	}
	muted := plugin.Style{FG: t.theme.Colors["document.lineNumber"]}
	if muted.FG == "" {
		muted.FG = "gray"
	}
	plain := plugin.Style{}
	selected := plugin.Style{FG: "brightwhite", BG: "blue", Bold: true}
	put := func(x, y, width int, s string, st plugin.Style) { f.Text(x, y, clipped(s, width), st) }
	if t.w < 48 || t.h < 12 {
		put(0, 0, t.w, "Todos needs at least 48 x 12", accent)
		put(0, 2, t.w, "Resize or q to close; data untouched.", plain)
		return f
	}
	for i, a := range []string{"New [N]", "Load [L]", "Close [C]"} {
		st := accent
		if t.mode == "tree" && t.top == 0 && t.action == i {
			st = selected
		}
		put(i*14, 0, 13, "[ "+a+" ]", st)
	}
	path := "No location loaded"
	if t.st != nil {
		path = t.st.Root
	}
	put(0, 1, t.w, path, muted)
	put(0, t.h-2, t.w, t.message, muted)
	put(0, t.h-1, t.w, "CR preview/toggle | a d s o O R M i ? | Ctrl+g cancel | q quit", muted)
	rows := t.h - 6
	switch t.mode {
	case "picker":
		split := t.w / 2
		verb := "Load"
		if t.pickerNew {
			verb = "New (scaffold)"
		}
		put(0, 2, split-1, "Recent / workspaces", accent)
		put(split, 2, t.w-split, verb+": "+t.dir, accent)
		leftStart := max(0, t.left-rows+1)
		for i := leftStart; i < min(len(t.locations), leftStart+rows); i++ {
			st := plain
			if t.pane == 0 && i == t.left {
				st = selected
			}
			put(0, i-leftStart+3, split-1, t.locations[i].Label, st)
		}
		rightStart := max(0, t.right-rows+1)
		for i := rightStart; i < min(len(t.entries), rightStart+rows); i++ {
			e := t.entries[i]
			label, st := e.Name+"/", plain
			if e.Todo {
				label, st = "* "+label, accent
			}
			if t.pane == 1 && i == t.right {
				st = selected
			}
			put(split, i-rightStart+3, t.w-split, label, st)
		}
		put(0, t.h-3, t.w, "Tab panes; CR browse/load; Space select; l load here; n new here; / path", accent)
	case "tree":
		if t.preview != nil {
			put(0, 2, t.w, "PREVIEW ONLY (host cannot open files): "+t.preview.Path, accent)
			lines := wrap(string(t.preview.Original), t.w)
			t.previewScroll = min(t.previewScroll, max(0, len(lines)-rows))
			for i := t.previewScroll; i < min(len(lines), t.previewScroll+rows); i++ {
				put(0, i-t.previewScroll+3, t.w, lines[i], plain)
			}
			put(0, t.h-3, t.w, "j/k or PgUp/PgDn scroll; i / Backspace returns to tree", accent)
			break
		}
		put(0, 2, t.w, "Six-status task tree", accent)
		if t.selected < t.scroll {
			t.scroll = t.selected
		}
		if t.selected >= t.scroll+rows {
			t.scroll = t.selected - rows + 1
		}
		for i := t.scroll; i < min(len(t.rows), t.scroll+rows); i++ {
			r := t.rows[i]
			prefix, st := "  ", plain
			if r.Group != "" {
				prefix, st = "+ ", accent
				if t.expanded[r.Group] {
					prefix = "- "
				}
			}
			if t.top == 1 && i == t.selected {
				st = selected
			}
			put(0, i-t.scroll+3, t.w, strings.Repeat("  ", r.Depth)+prefix+r.Label, st)
		}
	case "title", "description", "path":
		label := map[string]string{"title": "Add task: title (Enter continues)", "description": "Description (Enter saves; Ctrl+j adds a line)", "path": "Browse an absolute path or ~/path (Enter)"}[t.mode]
		put(0, 3, t.w, label, accent)
		lines := wrap(t.input+"_", t.w-2)
		start := max(0, len(lines)-(t.h-9))
		for i := start; i < len(lines); i++ {
			put(1, 5+i-start, t.w-2, lines[i], plain)
		}
	case "remove":
		put(0, 3, t.w, "Permanently remove this task file? y confirms; n cancels", accent)
		if t.pending != nil {
			put(0, 5, t.w, t.pending.Title, plain)
			put(0, 6, t.w, t.pending.Path, muted)
		}
	case "status":
		put(0, 3, t.w, "Select status: j/k or 1-6; Enter applies; Ctrl+g cancels", accent)
		for i, s := range store.Statuses {
			st := plain
			if i == t.status {
				st = selected
			}
			put(2, i+4, t.w-2, fmt.Sprintf("%d  %s", i+1, s), st)
		}
	case "help":
		lines := []string{
			"Standalone Todos — no events, mailbox, assignments or automation",
			"A (assignment), e (Vars): unavailable; use the owning nvim runtime",
			"CR: header expands; task shows in-plugin Markdown preview (not host navigation)",
			"i: preview/return; a: title + description; d: remove after y confirmation",
			"s: choose any of six statuses; o: toggle group; O: expand/collapse all",
			"R: read-only refresh; M/L: locations; N: scaffold; C: unload (no deletion)",
			"Tab: top actions/tree or picker panes; arrows / j k: selection",
			"Picker: CR browse; Space select; l load here; n scaffold here; / type path",
			"Ctrl+g cancels a form; Backspace/i exits preview; Esc hides; q quits",
			"Malformed/duplicate/symlink files block writes; repair externally, then R.",
			"No concurrent foreign writers: optimistic checks are not a shared transaction.",
			"automated: unassigned, no origin; empty templates stay inert.",
			"Leaving automated: remove template-only fields externally first.",
		}
		lines = append(lines, t.snap.Issues...)
		lines = wrap(strings.Join(lines, "\n"), t.w)
		t.helpScroll = min(t.helpScroll, max(0, len(lines)-rows))
		for i := t.helpScroll; i < min(len(lines), t.helpScroll+rows); i++ {
			put(0, i-t.helpScroll+3, t.w, lines[i], plain)
		}
		put(0, t.h-3, t.w, "j/k or PgUp/PgDn scroll; ? / Enter returns", accent)
	}
	return f
}

func wrap(s string, width int) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		r := []rune(clean(line))
		for len(r) > max(width, 1) {
			lines = append(lines, string(r[:max(width, 1)]))
			r = r[max(width, 1):]
		}
		lines = append(lines, string(r))
	}
	return lines
}
