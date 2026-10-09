package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownBodyAndFrontmatterPreservation(t *testing.T) {
	s, err := Scaffold(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := "\n<!-- user comment: keep exactly -->\n# Sample\n\nBody **markdown**\r\n\n```yaml\nstatus: untouched\n```\n\n"
	metadata := "# precise comment\ntags: [one, 'value'] # inline\nreview: ['doc.md']\ndescription: ignored frontmatter\n"
	data := []byte("---\n" + fixture("open") + metadata + "---\n" + body)
	path := filepath.Join(s.Root, "open", "sample.md")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	task := onlyTask(t, s)
	if task.Field("description") == "ignored frontmatter" {
		t.Fatal("frontmatter description used")
	}
	if err := s.SetStatus(task, "completed", testNow); err != nil {
		t.Fatal(err)
	}
	after := onlyTask(t, s)
	if !bytes.HasSuffix(after.Original, []byte(body)) || !bytes.Contains(after.Original, []byte(metadata)) {
		t.Fatal("body or allowed metadata/comment bytes changed")
	}
}

func TestCanonicalWalkIgnoresLegacyYAMLAndUnrelatedMarkdown(t *testing.T) {
	s, err := Scaffold(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"open/unrelated.yaml", "open/unrelated.yml", "notes.md", "open/nested/task.md", "archived/task.md"} {
		path := filepath.Join(s.Root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not a task"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("unrelated.yaml", filepath.Join(s.Root, "open", "ignored-link.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("2026-10-07-new", "New", "Description", testNow); err != nil {
		t.Fatal(err)
	}
	task := onlyTask(t, s)
	if filepath.Ext(task.Path) != ".md" || strings.Contains(string(task.Original), "\ndescription:") {
		t.Fatal("noncanonical new task")
	}
}

func TestCRLFAndLifecycleCommentsPreserved(t *testing.T) {
	s, task := seeded(t, "completed", "tags: ['verbatim'] # keep inline\nreview: ['doc.md']\n")
	data := strings.ReplaceAll(string(task.Original), "\n", "\r\n")
	data = strings.Replace(data, "status: completed", "status: 'completed'  # status note", 1)
	data = strings.Replace(data, "completed_at: 2026-04-01T00:00:00Z", "completed_at: '2026-04-01T00:00:00Z'  # completion note", 1)
	if err := os.WriteFile(task.Path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	task = onlyTask(t, s)
	if err := s.SetStatus(task, "open", testNow); err != nil {
		t.Fatal(err)
	}
	after := onlyTask(t, s)
	for _, exact := range []string{"status: open  # status note\r\n", "# completion note\r\n", "tags: ['verbatim'] # keep inline\r\nreview: ['doc.md']\r\n", task.Body} {
		if !strings.Contains(string(after.Original), exact) {
			t.Fatalf("lost exact bytes %q", exact)
		}
	}
}

func TestPopulatedTemplateCannotLoseMetadata(t *testing.T) {
	s, task := seeded(t, "automated", "condition:\n  - 'event: something'\nexecute:\n  - 'bash: echo inert'\nlast_fired_at: 2026-04-01T00:00:00Z\n")
	if err := s.SetStatus(task, "open", testNow); err == nil {
		t.Fatal("discarded template fields")
	}
	if !bytes.Equal(task.Original, onlyTask(t, s).Original) {
		t.Fatal("template changed")
	}
}
