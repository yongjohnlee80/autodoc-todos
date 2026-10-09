package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc-todos/store"
	"github.com/yongjohnlee80/autodoc/plugin"
)

func controller(t *testing.T) *todos {
	t.Helper()
	c := newTodos()
	c.recentFile = filepath.Join(t.TempDir(), "state", "locations.json")
	c.recents = nil
	c.dir = t.TempDir()
	c.lookup = func() ([]workspace, error) { return nil, nil }
	c.now = func() time.Time { return time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC) }
	c.w, c.h = 100, 24
	c.send = func(*plugin.Frame) error { return nil }
	return c
}

func key(c *todos, k string) { c.Key(plugin.Key{Key: k}) }

func frameText(rows []plugin.Row) string {
	var b strings.Builder
	for _, row := range rows {
		for _, run := range row {
			b.WriteString(run.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func selectTask(t *testing.T, c *todos) {
	t.Helper()
	for i, row := range c.rows {
		if row.Task != nil {
			c.selected, c.top = i, 1
			return
		}
	}
	t.Fatal("no task row")
}

func TestControllerAddPreviewStatusRemove(t *testing.T) {
	c := controller(t)
	c.load(c.dir, true)
	if c.st == nil || len(c.rows) != 6 {
		t.Fatalf("scaffold: %s", c.message)
	}
	key(c, "a")
	c.Key(plugin.Key{Text: "A unicode task ✓"})
	key(c, "Enter")
	c.Key(plugin.Key{Text: "First line"})
	c.Key(plugin.Key{Key: "j", Ctrl: true})
	c.Key(plugin.Key{Text: "Second line"})
	key(c, "CR")
	if c.mode != "tree" || len(c.snap.Tasks) != 1 || c.task().Field("description") != "First line\nSecond line" {
		t.Fatalf("add: mode=%s snapshot=%+v", c.mode, c.snap)
	}
	original := c.task()
	key(c, "CR")
	if c.preview != original || !strings.Contains(frameText(c.render().Rows()), "PREVIEW ONLY") {
		t.Fatal("CR did not show honest in-plugin preview")
	}
	key(c, "i")
	if c.preview != nil {
		t.Fatal("i did not return to tree")
	}
	key(c, "A")
	if !strings.Contains(c.message, "unavailable") {
		t.Fatal("assignment limitation not reported")
	}
	key(c, "e")
	if !strings.Contains(c.message, "unavailable") {
		t.Fatal("Vars limitation not reported")
	}
	key(c, "s")
	key(c, "3")
	key(c, "Enter")
	if c.snap.Tasks[0].Status != "automated" || c.snap.Tasks[0].Field("condition") != "" || c.snap.Tasks[0].Field("execute") != "" {
		t.Fatal("empty inert template transition failed")
	}
	selectTask(t, c)
	key(c, "s")
	key(c, "5")
	key(c, "Enter")
	if c.snap.Tasks[0].Status != "completed" || c.snap.Tasks[0].Field("updated") != original.Field("updated") {
		t.Fatal("completion lifecycle incorrect")
	}
	selectTask(t, c)
	key(c, "d")
	key(c, "Enter")
	if _, err := os.Stat(c.pending.Path); err != nil {
		t.Fatal("remove happened without y confirmation")
	}
	key(c, "n")
	if c.mode != "tree" || len(c.snap.Tasks) != 1 {
		t.Fatal("remove cancel failed")
	}
	key(c, "d")
	key(c, "y")
	if len(c.snap.Tasks) != 0 {
		t.Fatal("confirmed remove failed")
	}
	if _, err := os.Stat(filepath.Join(c.st.Root, "completed")); err != nil {
		t.Fatal("remove deleted a directory")
	}
}

func TestIndependentLocationsAndTopActions(t *testing.T) {
	c := controller(t)
	external := c.dir
	workspaceRoot := t.TempDir()
	c.lookup = func() ([]workspace, error) {
		return []workspace{{Name: "WorkspaceName", Root: workspaceRoot}}, nil
	}
	c.load(external, true)
	root := c.st.Root
	c.Document(plugin.Document{Workspace: "WorkspaceName", Path: "note.md"})
	if c.st.Root != root || c.dir != external {
		t.Fatal("document feed switched external store or treated name as path")
	}
	key(c, "M")
	if c.mode != "picker" || c.locations[0].Path != workspaceRoot || c.locations[1].Path != root {
		t.Fatalf("locations not resolved: %+v", c.locations)
	}
	c.pane, c.left = 0, 0
	key(c, "CR")
	if c.st.Root != root || c.mode != "picker" {
		t.Fatal("failed load replaced active location")
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, ".todo-list")); !os.IsNotExist(err) {
		t.Fatal("Load scaffolded workspace")
	}
	key(c, "N")
	if c.st.Root != root {
		t.Fatal("picker New ignored current browser directory")
	}
	key(c, "Tab")
	c.action = 2
	key(c, "CR")
	if c.st != nil || !strings.Contains(c.message, "files untouched") {
		t.Fatal("top Close did not unload")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("Close deleted location")
	}
	key(c, "L")
	c.pane, c.left = 0, 1
	key(c, "CR")
	if c.st == nil || c.st.Root != root {
		t.Fatal("recent store did not reload")
	}
	data, err := os.ReadFile(c.recentFile)
	if err != nil || !strings.Contains(string(data), ".todo-list") {
		t.Fatal("recents not persisted")
	}
}

func TestArchiveTreeAndToggleAll(t *testing.T) {
	c := controller(t)
	c.load(c.dir, true)
	if err := c.st.Add("archived-task", "Archive me", "", c.now()); err != nil {
		t.Fatal(err)
	}
	c.refresh()
	if err := c.st.SetStatus(c.snap.Tasks[0], "archived", c.now()); err != nil {
		t.Fatal(err)
	}
	c.refresh()
	if len(c.rows) != 6 {
		t.Fatal("archive should initially be collapsed")
	}
	c.selected = 5
	key(c, "CR")
	if len(c.rows) != 7 || c.rows[6].Group != "archived/2026" {
		t.Fatal("year group missing")
	}
	c.selected = 6
	key(c, "o")
	if len(c.rows) != 8 || c.rows[7].Group != "archived/2026/10" {
		t.Fatal("month group missing")
	}
	key(c, "O")
	if len(c.rows) != 9 || c.rows[8].Task == nil {
		t.Fatal("O did not expand nested archive")
	}
	key(c, "O")
	if len(c.rows) != 6 {
		t.Fatal("O did not collapse all")
	}
}

func TestResizeThemeAndCancel(t *testing.T) {
	c := controller(t)
	c.load(c.dir, true)
	key(c, "a")
	c.Key(plugin.Key{Text: "✓x"})
	key(c, "Backspace")
	if c.input != "✓" {
		t.Fatal("backspace is not rune-safe")
	}
	c.Key(plugin.Key{Key: "g", Ctrl: true})
	if c.mode != "tree" || len(c.snap.Tasks) != 0 {
		t.Fatal("cancel saved task")
	}
	for _, size := range [][2]int{{0, 0}, {20, 5}, {48, 12}, {100, 30}} {
		c.Resize(size[0], size[1])
		w, h := c.render().Size()
		if w != size[0] || h != size[1] {
			t.Fatal("incorrect frame dimensions")
		}
	}
	c.Theme(plugin.Theme{Colors: map[string]string{"document.cursor": "#123456"}})
	if c.render().Rows()[0][0].Style.FG != "#123456" {
		t.Fatal("theme not applied")
	}
}

func TestHelpScrollsToIssues(t *testing.T) {
	c := controller(t)
	c.picker(false)
	c.w, c.h = 48, 12
	c.snap.Issues = []string{"specific malformed store issue"}
	key(c, "?")
	for range 20 {
		key(c, "PageDown")
	}
	if !strings.Contains(frameText(c.render().Rows()), "specific malformed store issue") {
		t.Fatal("store issue not reachable in small dialog")
	}
	c.Key(plugin.Key{Key: "g", Ctrl: true})
	if c.mode != "picker" {
		t.Fatal("help cancel lost picker context")
	}
}

type testHost struct {
	link   *plugin.Link
	frames chan []plugin.Row
	ready  chan int
	closed chan struct{}
	done   chan error
}

func startHost(t *testing.T, c *todos) *testHost {
	t.Helper()
	toPlugin, fromHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromPlugin, toHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hs := &testHost{frames: make(chan []plugin.Row, 64), ready: make(chan int, 1), closed: make(chan struct{}, 1), done: make(chan error, 1)}
	go func() { hs.done <- plugin.ServeConn(ctx, plugin.FileConn(toPlugin, toHost), c) }()
	hs.link, err = plugin.NewLink(ctx, plugin.FileConn(fromPlugin, fromHost), func(method string, params []any) {
		switch method {
		case plugin.MethodFrame:
			rows, _, err := plugin.ReadFrame(params)
			if err == nil {
				hs.frames <- rows
			}
		case plugin.MethodReady:
			v, err := plugin.ReadReady(params)
			if err == nil {
				hs.ready <- v
			}
		case plugin.MethodHostClose:
			hs.closed <- struct{}{}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); hs.link.Close() })
	hs.send(t, plugin.MethodOpen, plugin.OpenParams(plugin.Open{Protocol: 2, Width: 100, Height: 24}))
	select {
	case v := <-hs.ready:
		if v != 2 {
			t.Fatalf("protocol: %d", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no host.ready")
	}
	return hs
}

func (h *testHost) send(t *testing.T, method string, params []any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.link.Notify(ctx, method, params); err != nil {
		t.Fatal(err)
	}
}

func (h *testHost) until(t *testing.T, want string) []plugin.Row {
	t.Helper()
	deadline := time.After(5 * time.Second)
	last := ""
	for {
		select {
		case rows := <-h.frames:
			last = frameText(rows)
			if strings.Contains(last, want) {
				return rows
			}
		case <-deadline:
			t.Fatalf("no frame containing %q; last:\n%s", want, last)
		}
	}
}

func TestProtocol2Lifecycle(t *testing.T) {
	c := controller(t)
	root := c.dir
	hs := startHost(t, c)
	hs.until(t, "Recent / workspaces")
	hs.send(t, plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: "N"}))
	hs.until(t, "Six-status task tree")
	hs.send(t, plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: "a"}))
	hs.until(t, "Add task: title")
	hs.send(t, plugin.MethodKey, plugin.KeyParams(plugin.Key{Text: "Wire task"}))
	hs.send(t, plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: "Enter"}))
	hs.until(t, "Description")
	hs.send(t, plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: "Enter"}))
	hs.until(t, "Wire task")
	hs.send(t, plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: "CR"}))
	hs.until(t, "PREVIEW ONLY")
	hs.send(t, plugin.MethodDocument, plugin.DocumentParams(plugin.Document{Workspace: "not-a-path", Path: "ignored.md"}))
	hs.until(t, "PREVIEW ONLY")
	hs.send(t, plugin.MethodResize, plugin.ResizeParams(20, 5))
	rows := hs.until(t, "Todos needs")
	if len(rows) != 5 {
		t.Fatal("resize not received")
	}
	hs.send(t, plugin.MethodResize, plugin.ResizeParams(100, 24))
	hs.until(t, "PREVIEW ONLY")
	hs.send(t, plugin.MethodTheme, plugin.ThemeParams(plugin.Theme{Colors: map[string]string{"document.cursor": "#abcdef"}}))
	rows = hs.until(t, "PREVIEW ONLY")
	if rows[0][0].Style.FG != "#abcdef" {
		t.Fatal("theme not received")
	}
	hs.send(t, plugin.MethodCommand, plugin.CommandParams("refresh"))
	hs.until(t, "Refreshed: 1 tasks")
	hs.send(t, plugin.MethodHide, plugin.EmptyParams())
	hs.send(t, plugin.MethodShow, plugin.EmptyParams())
	hs.until(t, "Wire task")
	hs.send(t, plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: "q"}))
	select {
	case <-hs.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("q did not send host.close")
	}
	hs.send(t, plugin.MethodClose, plugin.EmptyParams())
	select {
	case err := <-hs.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plugin.close did not finish")
	}
	s, err := store.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.Scan()
	if err != nil || len(snap.Tasks) != 1 || snap.Tasks[0].Title != "Wire task" {
		t.Fatal("protocol edits not persisted")
	}
}
