package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type coreTask struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

type coreScan struct {
	Tasks  []coreTask `json:"tasks"`
	Issues []string   `json:"issues"`
	Fields []string   `json:"fields"`
}

// Runs only decoder/schema/path helpers, with user config, plugins and shada disabled.
// All store access in Lua is read-only; it never invokes the core writer/runtime.
func scanWithCore(t *testing.T, root string) coreScan {
	t.Helper()
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("Neovim not installed")
	}
	core := os.Getenv("AUTODOC_TODOS_CORE_RUNTIME")
	if core == "" {
		core, _ = filepath.Abs("../../../auto-core.nvim/main")
	}
	if _, err := os.Stat(filepath.Join(core, "lua", "auto-core", "todo", "md.lua")); err != nil {
		t.Skip("canonical core runtime unavailable; set AUTODOC_TODOS_CORE_RUNTIME")
	}
	lua := `local ok, err = pcall(function()
  vim.opt.runtimepath:append(vim.env.AUTODOC_TODOS_CORE_RUNTIME)
  local md = require("auto-core.todo.md")
  local schema = require("auto-core.todo.schema")
  local paths = require("auto-core.todo.paths")
  local out = {tasks = {}, issues = {}, fields = vim.deepcopy(schema.FRONTMATTER_ORDER)}
  table.insert(out.fields, "description")
  paths.walk(vim.env.AUTODOC_TODOS_SCAN_ROOT, function(path)
    local f = assert(io.open(path, "rb"))
    local src = f:read("*a"); f:close()
    local d = md.decode(src)
    local v = d.ok and schema.validate(d.value) or d
    if not v.ok then table.insert(out.issues, path .. ": " .. tostring(v.err))
    else table.insert(out.tasks, {id=d.value.id, status=d.value.status, description=d.value.description, path=path}) end
  end)
  print("AUTODOC_CORE_SCAN=" .. vim.json.encode(out))
end)
if not ok then print(err); vim.cmd("cquit 1") end`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nvim, "--headless", "--noplugin", "-u", "NONE", "-i", "NONE", "-n", "-c", "lua "+lua, "-c", "qa!")
	cmd.Env = append(os.Environ(), "AUTODOC_TODOS_CORE_RUNTIME="+core, "AUTODOC_TODOS_SCAN_ROOT="+root)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("core decoder: %v\n%s", err, data)
	}
	_, result, ok := strings.Cut(string(data), "AUTODOC_CORE_SCAN=")
	if !ok {
		t.Fatalf("missing core result: %s", data)
	}
	var scan coreScan
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &scan); err != nil {
		t.Fatalf("core JSON: %v: %s", err, result)
	}
	return scan
}

func TestGoCreatedMarkdownDecodesInCanonicalLua(t *testing.T) {
	s, err := Scaffold(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desc := "Prose **markdown**\n\n<!-- personal comment -->\n```lua\nprint('hello')\n```"
	for _, status := range Statuses {
		id := "2026-10-07-" + status
		if err := s.Add(id, "Task "+status, desc, testNow); err != nil {
			t.Fatal(err)
		}
		snap, err := s.Scan()
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range snap.Tasks {
			if task.ID == id {
				if err := s.SetStatus(task, status, testNow); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	core := scanWithCore(t, s.Root)
	if len(core.Issues) != 0 || len(core.Tasks) != 6 {
		t.Fatalf("canonical decode/schema: %+v", core)
	}
	for _, task := range core.Tasks {
		if task.Description != desc || task.ID != "2026-10-07-"+task.Status {
			t.Fatalf("canonical task mismatch: %+v", task)
		}
		data, err := os.ReadFile(task.Path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\ndescription:") || strings.Count(string(data), "<!-- ─── auto-core.todo schema v1") != 1 {
			t.Fatal("description serialized in frontmatter or duplicate marker")
		}
	}
}

func TestCanonicalTopLevelFieldCatalog(t *testing.T) {
	s, err := Scaffold(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	core := scanWithCore(t, s.Root)
	fields := make([]string, 0, len(topLevelFields))
	for key := range topLevelFields {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	sort.Strings(core.Fields)
	if !reflect.DeepEqual(fields, core.Fields) {
		t.Fatalf("top-level catalog drift: Go=%v; Lua=%v", fields, core.Fields)
	}
}

func TestUnknownTopLevelFieldsCanonicalLuaParity(t *testing.T) {
	s, err := Scaffold(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range unknownFields {
		path := filepath.Join(s.Root, "open", fmt.Sprintf("bad-%d.md", i))
		data := []byte(strings.Replace(markdownFixture("open", tc.key+": "+tc.value+"\n"), "id: sample", fmt.Sprintf("id: bad-%d", i), 1))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	core := scanWithCore(t, s.Root)
	if len(core.Tasks) != 0 || len(core.Issues) != len(unknownFields) {
		t.Fatalf("canonical Lua accepted unknown fields: %+v", core)
	}
	goScan, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(goScan.Tasks) != 0 || len(goScan.Issues) != len(core.Issues) {
		t.Fatalf("unknown-field parity: Go=%+v; Lua=%+v", goScan, core)
	}
}

func TestRealStoreReadOnlyParity(t *testing.T) {
	root := os.Getenv("AUTODOC_TODOS_REAL_STORE")
	if root == "" {
		t.Skip("opt-in read-only scan: set AUTODOC_TODOS_REAL_STORE")
	}
	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goScan, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	core := scanWithCore(t, s.Root)
	byPath := map[string]*Task{}
	counts := map[string]int{}
	for _, task := range goScan.Tasks {
		byPath[task.Path] = task
		counts[task.Status]++
	}
	t.Logf("read-only parity: Go tasks=%d archived=%d issues=%d; core tasks=%d issues=%d; statuses=%v", len(goScan.Tasks), counts["archived"], len(goScan.Issues), len(core.Tasks), len(core.Issues), counts)
	if len(goScan.Issues) != 0 || len(core.Issues) != 0 {
		t.Fatalf("Go issues=%v; core issues=%v", goScan.Issues, core.Issues)
	}
	if len(goScan.Tasks) != len(core.Tasks) {
		t.Fatal("task counts differ")
	}
	for _, task := range core.Tasks {
		g := byPath[task.Path]
		if g == nil || g.ID != task.ID || g.Status != task.Status || g.Field("description") != task.Description {
			t.Errorf("decoder mismatch: %s", task.Path)
		}
	}
}
