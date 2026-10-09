// autodoc-todos is a standalone AutoDoc protocol-2 dialog over Markdown task files.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc-todos/browser"
	"github.com/yongjohnlee80/autodoc-todos/store"
	"github.com/yongjohnlee80/autodoc/plugin"
)

func main() {
	if err := plugin.Serve(context.Background(), newTodos()); err != nil {
		fmt.Fprintln(os.Stderr, "autodoc-todos:", err)
		os.Exit(1)
	}
}

type treeRow struct {
	Label, Group string
	Depth        int
	Task         *store.Task
}

type location struct{ Label, Path string }

// Serve invokes the controller serially; there are no worker goroutines or automation hooks.
type todos struct {
	peer              *plugin.Peer
	send              func(*plugin.Frame) error
	closeHost         func() error
	w, h              int
	theme             plugin.Theme
	st                *store.Store
	snap              store.Snapshot
	expanded          map[string]bool
	rows              []treeRow
	selected, scroll  int
	top, action       int
	mode, message     string
	preview           *store.Task
	previewScroll     int
	helpScroll        int
	pending           *store.Task
	status            int
	input, title      string
	returnMode        string
	pickerNew         bool
	dir               string
	entries           []browser.Entry
	locations         []location
	pane, left, right int
	recents           []string
	recentFile        string
	workspaceName     string
	lookup            func() ([]workspace, error)
	now               func() time.Time
}

var _ plugin.Handler = (*todos)(nil)
var _ plugin.DocumentReader = (*todos)(nil)
var _ plugin.Commander = (*todos)(nil)

func newTodos() *todos {
	dir, _ := os.Getwd()
	t := &todos{dir: dir, expanded: map[string]bool{}, lookup: listWorkspaces, now: time.Now, recentFile: recentPath()}
	for _, status := range store.Statuses {
		t.expanded[status] = status != "archived"
	}
	if b, err := os.ReadFile(t.recentFile); err == nil {
		_ = json.Unmarshal(b, &t.recents)
		if len(t.recents) > 20 {
			t.recents = t.recents[:20]
		}
	}
	return t
}

func (t *todos) Open(p *plugin.Peer, o plugin.Open) {
	t.peer, t.send, t.closeHost = p, p.Frame, p.Close
	t.w, t.h, t.theme = o.Width, o.Height, o.Theme
	t.picker(false)
	t.draw()
}

func (t *todos) Resize(w, h int)       { t.w, t.h = w, h; t.draw() }
func (t *todos) Theme(th plugin.Theme) { t.theme = th; t.draw() }
func (t *todos) Close()                { t.pending = nil }
func (t *todos) Hide()                 {}
func (t *todos) Show()                 { t.draw() }

func (t *todos) Document(d plugin.Document) {
	// Never switch the independently loaded store when the host changes documents/workspaces.
	if t.workspaceName != d.Workspace {
		t.workspaceName = d.Workspace
		if t.mode == "picker" {
			t.populateLocations()
		}
	}
	t.draw()
}

func (t *todos) Command(id string) {
	switch id {
	case "locations":
		t.picker(false)
	case "new":
		t.picker(true)
	case "refresh":
		t.refresh()
	}
	t.draw()
}

func (t *todos) picker(create bool) {
	t.mode, t.pickerNew = "picker", create
	t.pending, t.preview = nil, nil
	t.populateLocations()
	t.browse(t.dir)
}

func (t *todos) populateLocations() {
	t.locations = nil
	all, err := t.lookup()
	if err != nil {
		t.message = "Workspace discovery: " + err.Error()
	}
	for _, w := range all {
		if w.Name == t.workspaceName {
			t.locations = append(t.locations, location{"Current workspace: " + w.Name, w.Root})
		}
	}
	for _, p := range t.recents {
		t.locations = append(t.locations, location{"Recent: " + filepath.Dir(p), p})
	}
	for _, w := range all {
		if w.Name != t.workspaceName {
			t.locations = append(t.locations, location{"Workspace: " + w.Name, w.Root})
		}
	}
	t.left = min(t.left, max(0, len(t.locations)-1))
}

func (t *todos) browse(dir string) {
	entries, err := browser.List(dir)
	if err != nil {
		t.message = err.Error()
		return
	}
	t.dir, _ = filepath.Abs(dir)
	t.entries, t.right = entries, 0
}

func (t *todos) load(path string, create bool) {
	var s *store.Store
	var err error
	if create {
		s, err = store.Scaffold(path)
	} else {
		s, err = store.Load(path)
	}
	if err != nil {
		t.message = err.Error()
		return
	}
	snap, err := s.Scan()
	if err != nil {
		t.message = err.Error()
		return
	}
	t.st, t.snap, t.mode = s, snap, "tree"
	t.preview, t.pending, t.selected, t.scroll, t.top = nil, nil, 0, 0, 1
	t.message = "Loaded " + s.Root
	if len(snap.Issues) != 0 {
		t.message = fmt.Sprintf("Read-only: %d store issues; ? for details", len(snap.Issues))
	}
	t.rebuild()
	t.remember(s.Root)
}

func (t *todos) refresh() {
	if t.mode == "picker" {
		t.populateLocations()
		t.browse(t.dir)
		return
	}
	if t.st == nil {
		return
	}
	snap, err := t.st.Scan()
	if err != nil {
		t.message = err.Error()
		return
	}
	id := ""
	if task := t.task(); task != nil {
		id = task.ID
	}
	t.snap, t.preview, t.previewScroll = snap, nil, 0
	t.rebuild()
	for i, row := range t.rows {
		if row.Task != nil && row.Task.ID == id {
			t.selected = i
		}
	}
	t.message = fmt.Sprintf("Refreshed: %d tasks, %d issues", len(snap.Tasks), len(snap.Issues))
}

func archiveGroup(task *store.Task) string {
	s := task.Field("archived_at")
	if len(s) >= 7 {
		return strings.ReplaceAll(s[:7], "-", "/")
	}
	return "unknown"
}

func (t *todos) rebuild() {
	t.rows = nil
	for _, status := range store.Statuses {
		var tasks []*store.Task
		for _, task := range t.snap.Tasks {
			if task.Status == status {
				tasks = append(tasks, task)
			}
		}
		t.rows = append(t.rows, treeRow{Label: fmt.Sprintf("%s (%d)", status, len(tasks)), Group: status})
		if !t.expanded[status] {
			continue
		}
		if status != "archived" {
			for _, task := range tasks {
				t.rows = append(t.rows, treeRow{Label: task.Title, Task: task, Depth: 1})
			}
			continue
		}
		groups := map[string][]*store.Task{}
		for _, task := range tasks {
			g := archiveGroup(task)
			groups[g] = append(groups[g], task)
		}
		var years []string
		months := map[string][]string{}
		for g := range groups {
			year := strings.Split(g, "/")[0]
			if _, ok := months[year]; !ok {
				years = append(years, year)
			}
			months[year] = append(months[year], g)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(years)))
		for _, year := range years {
			yg := "archived/" + year
			t.rows = append(t.rows, treeRow{Label: year, Group: yg, Depth: 1})
			if !t.expanded[yg] {
				continue
			}
			sort.Sort(sort.Reverse(sort.StringSlice(months[year])))
			for _, month := range months[year] {
				mg := "archived/" + month
				t.rows = append(t.rows, treeRow{Label: strings.TrimPrefix(month, year+"/"), Group: mg, Depth: 2})
				if t.expanded[mg] {
					for _, task := range groups[month] {
						t.rows = append(t.rows, treeRow{Label: task.Title, Task: task, Depth: 3})
					}
				}
			}
		}
	}
	t.selected = min(t.selected, max(0, len(t.rows)-1))
}

func (t *todos) task() *store.Task {
	if t.selected >= 0 && t.selected < len(t.rows) {
		return t.rows[t.selected].Task
	}
	return nil
}

func (t *todos) toggle() {
	if t.selected < len(t.rows) && t.rows[t.selected].Group != "" {
		g := t.rows[t.selected].Group
		t.expanded[g] = !t.expanded[g]
		t.rebuild()
	}
}

func (t *todos) all() {
	expand := false
	for _, s := range store.Statuses {
		expand = expand || !t.expanded[s]
	}
	if !expand {
		for _, task := range t.snap.Tasks {
			if task.Status == "archived" {
				g := archiveGroup(task)
				expand = expand || !t.expanded["archived/"+g] || !t.expanded["archived/"+strings.Split(g, "/")[0]]
			}
		}
	}
	for _, s := range store.Statuses {
		t.expanded[s] = expand
	}
	for _, task := range t.snap.Tasks {
		if task.Status == "archived" {
			g := archiveGroup(task)
			t.expanded["archived/"+g], t.expanded["archived/"+strings.Split(g, "/")[0]] = expand, expand
		}
	}
	t.rebuild()
}

func (t *todos) Key(k plugin.Key) {
	defer t.draw()
	if k.Ctrl && k.Key == "g" {
		t.cancel()
		return
	}
	if t.mode == "title" || t.mode == "description" || t.mode == "path" {
		t.formKey(k)
		return
	}
	if t.mode == "remove" {
		if k.Key == "y" && t.pending != nil && t.st != nil {
			err := t.st.Remove(t.pending)
			t.mode, t.pending = "tree", nil
			t.refresh()
			if err != nil {
				t.message = err.Error()
			}
		} else if k.Key == "n" || k.Key == "Backspace" {
			t.cancel()
		}
		return
	}
	if t.mode == "status" {
		switch k.Key {
		case "j", "Down":
			t.status = min(t.status+1, len(store.Statuses)-1)
		case "k", "Up":
			t.status = max(t.status-1, 0)
		case "Backspace":
			t.cancel()
		case "Enter", "CR":
			if t.pending != nil && t.st != nil {
				err := t.st.SetStatus(t.pending, store.Statuses[t.status], t.now())
				t.mode, t.pending = "tree", nil
				t.refresh()
				if err != nil {
					t.message = err.Error()
				}
			}
		default:
			if k.Key >= "1" && k.Key <= "6" {
				t.status = int(k.Key[0] - '1')
			}
		}
		return
	}
	if t.mode == "help" {
		switch k.Key {
		case "?", "Backspace", "Enter", "CR":
			t.mode = t.returnMode
		case "j", "Down":
			t.helpScroll++
		case "k", "Up":
			t.helpScroll = max(0, t.helpScroll-1)
		case "PageDown":
			t.helpScroll += max(1, t.h-6)
		case "PageUp":
			t.helpScroll = max(0, t.helpScroll-max(1, t.h-6))
		}
		return
	}
	if k.Key == "?" {
		t.returnMode, t.mode = t.mode, "help"
		t.helpScroll = 0
		return
	}
	if k.Key == "q" {
		if t.closeHost != nil {
			_ = t.closeHost()
		}
		return
	}
	if t.mode == "picker" {
		t.pickerKey(k)
		return
	}
	switch k.Key {
	case "N":
		t.picker(true)
	case "L", "M":
		t.picker(false)
	case "C":
		t.unload()
	case "Tab":
		t.top = 1 - t.top
	case "Left", "h":
		if t.top == 0 {
			t.action = max(0, t.action-1)
		}
	case "Right", "l":
		if t.top == 0 {
			t.action = min(2, t.action+1)
		}
	case "Down", "j":
		if t.preview != nil {
			t.previewScroll++
		} else {
			t.top = 1
			t.selected = min(t.selected+1, max(0, len(t.rows)-1))
		}
	case "Up", "k":
		if t.preview != nil {
			t.previewScroll = max(0, t.previewScroll-1)
		} else {
			t.selected = max(0, t.selected-1)
		}
	case "PageDown":
		if t.preview != nil {
			t.previewScroll += max(1, t.h-7)
		} else {
			t.selected = min(t.selected+max(1, t.h-7), max(0, len(t.rows)-1))
		}
	case "PageUp":
		if t.preview != nil {
			t.previewScroll = max(0, t.previewScroll-max(1, t.h-7))
		} else {
			t.selected = max(0, t.selected-max(1, t.h-7))
		}
	case "Enter", "CR":
		if t.top == 0 {
			switch t.action {
			case 0:
				t.picker(true)
			case 1:
				t.picker(false)
			case 2:
				t.unload()
			}
		} else if task := t.task(); task != nil {
			t.preview, t.previewScroll = task, 0
		} else {
			t.toggle()
		}
	case "i":
		if t.preview != nil {
			t.preview = nil
		} else {
			t.preview, t.previewScroll = t.task(), 0
		}
	case "Backspace":
		t.preview = nil
	case "o":
		t.toggle()
	case "O":
		t.all()
	case "R":
		t.refresh()
	case "a":
		if t.st != nil {
			t.mode, t.input, t.title = "title", "", ""
		}
	case "d":
		if task := t.task(); task != nil {
			t.pending, t.mode = task, "remove"
		}
	case "s":
		if task := t.task(); task != nil {
			t.pending, t.mode = task, "status"
			for i, s := range store.Statuses {
				if s == task.Status {
					t.status = i
				}
			}
		}
	case "A":
		t.message = "A assignment unavailable: use the owning nvim runtime (no mailbox side effects here)."
	case "e":
		t.message = "e Vars unavailable: use the owning nvim runtime; existing variables are preserved."
	}
}

func (t *todos) unload() {
	t.st, t.preview, t.pending, t.rows = nil, nil, nil, nil
	t.snap, t.mode, t.message = store.Snapshot{}, "tree", "Location closed; files untouched. L to load, N to create."
}

func (t *todos) cancel() {
	if t.mode == "help" {
		t.mode = t.returnMode
	} else if t.mode == "path" {
		t.mode = "picker"
	} else {
		t.mode = "tree"
	}
	t.pending, t.input = nil, ""
}

func (t *todos) formKey(k plugin.Key) {
	if k.Key == "Backspace" {
		r := []rune(t.input)
		if len(r) > 0 {
			t.input = string(r[:len(r)-1])
		}
		return
	}
	if t.mode == "description" && ((k.Ctrl && k.Key == "j") || (k.Shift && k.Key == "Enter")) {
		t.input += "\n"
		return
	}
	if k.Key == "Enter" || k.Key == "CR" {
		switch t.mode {
		case "path":
			p := t.input
			if p == "~" || strings.HasPrefix(p, "~/") {
				if root, err := store.Location(p); err == nil {
					p = filepath.Dir(root)
				}
			}
			t.mode = "picker"
			t.browse(p)
		case "title":
			if strings.TrimSpace(t.input) == "" {
				t.message = "Title is required"
				return
			}
			t.title, t.input, t.mode = strings.TrimSpace(t.input), "", "description"
		case "description":
			now := t.now()
			id, err := store.NewID(t.title, now)
			if err == nil {
				err = t.st.Add(id, t.title, t.input, now)
			}
			if err != nil {
				t.message = err.Error()
				return
			}
			t.mode = "tree"
			t.expanded["open"] = true
			t.refresh()
			for i, row := range t.rows {
				if row.Task != nil && row.Task.ID == id {
					t.selected = i
				}
			}
		}
		return
	}
	if !k.Ctrl && !k.Alt {
		s := k.Text
		if s == "" && len([]rune(k.Key)) == 1 {
			s = k.Key
		}
		s = strings.Map(func(r rune) rune {
			if r < 32 && !(r == '\n' && t.mode == "description") {
				return -1
			}
			if r == 127 {
				return -1
			}
			return r
		}, s)
		if len(t.input)+len(s) <= 64*1024 {
			t.input += s
		}
	}
}

func (t *todos) pickerKey(k plugin.Key) {
	switch k.Key {
	case "Tab":
		t.pane = 1 - t.pane
	case "Left", "h":
		t.pane = 0
	case "Right":
		t.pane = 1
	case "Down", "j":
		if t.pane == 0 {
			t.left = min(t.left+1, max(0, len(t.locations)-1))
		} else {
			t.right = min(t.right+1, max(0, len(t.entries)-1))
		}
	case "Up", "k":
		if t.pane == 0 {
			t.left = max(t.left-1, 0)
		} else {
			t.right = max(t.right-1, 0)
		}
	case "Enter", "CR":
		if t.pane == 0 && len(t.locations) > 0 {
			t.load(t.locations[t.left].Path, t.pickerNew)
		} else if t.pane == 1 && len(t.entries) > 0 {
			e := t.entries[t.right]
			if e.Name == ".todo-list" {
				t.load(e.Path, t.pickerNew)
			} else {
				t.browse(e.Path)
			}
		}
	case " ":
		if t.pane == 0 && len(t.locations) > 0 {
			t.load(t.locations[t.left].Path, t.pickerNew)
		} else if len(t.entries) > 0 {
			t.load(t.entries[t.right].Path, t.pickerNew)
		}
	case "l", "L":
		t.load(t.dir, false)
	case "n", "N":
		t.load(t.dir, true)
	case "C":
		t.unload()
	case "M":
		t.picker(false)
	case "/":
		t.mode, t.input = "path", ""
	case "Backspace":
		t.browse(filepath.Dir(t.dir))
	case "R":
		t.refresh()
	}
}

func recentPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "autodoc-todos", "locations.json")
}

func (t *todos) remember(root string) {
	recents := []string{root}
	for _, p := range t.recents {
		if p != root && len(recents) < 20 {
			recents = append(recents, p)
		}
	}
	t.recents = recents
	if t.recentFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(t.recentFile), 0o700); err != nil {
		t.message += "; recent locations: " + err.Error()
		return
	}
	b, _ := json.Marshal(recents)
	f, err := os.CreateTemp(filepath.Dir(t.recentFile), ".locations-*.tmp")
	if err != nil {
		t.message += "; recent locations: " + err.Error()
		return
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), t.recentFile)
	}
	if err != nil {
		t.message += "; recent locations: " + err.Error()
	}
}
