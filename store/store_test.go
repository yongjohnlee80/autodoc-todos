package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 7, 12, 30, 0, 0, time.UTC)

func fixture(status string) string {
	return fmt.Sprintf("id: sample\nversion: 1\nstatus: %s\ntitle: Sample\ncreated: 2026-01-01T00:00:00Z\nupdated: 2026-02-01T00:00:00Z\nstatus_changed: 2026-03-01T00:00:00Z\n", status)
}

func markdownFixture(status, extra string) string {
	if status == "completed" {
		extra = "completed_at: 2026-04-01T00:00:00Z\n" + extra
	}
	if status == "archived" {
		extra = "archived_at: 2026-04-02T00:00:00Z\n" + extra
	}
	return "---\n" + fixture(status) + extra + "---\n\n# Sample\n\nBody description\n"
}

func seeded(t *testing.T, status, extra string) (*Store, *Task) {
	t.Helper()
	s, err := Scaffold(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rel := status
	if status == "archived" {
		rel = filepath.Join(status, "2026", "04")
	}
	if err := s.ensureDir(rel); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, rel, "sample.md")
	if err := os.WriteFile(path, []byte(markdownFixture(status, extra)), 0o640); err != nil {
		t.Fatal(err)
	}
	return s, onlyTask(t, s)
}

func onlyTask(t *testing.T, s *Store) *Task {
	t.Helper()
	snap, err := s.Scan()
	if err != nil || len(snap.Issues) != 0 || len(snap.Tasks) != 1 {
		t.Fatalf("scan: %+v, %v", snap, err)
	}
	return snap.Tasks[0]
}

func TestStatusTransitions(t *testing.T) {
	for _, from := range Statuses {
		for _, to := range Statuses {
			t.Run(from+"_to_"+to, func(t *testing.T) {
				extra := ""
				s, before := seeded(t, from, extra)
				if err := s.SetStatus(before, to, testNow); err != nil {
					t.Fatal(err)
				}
				after := onlyTask(t, s)
				if from == to {
					if !bytes.Equal(before.Original, after.Original) || before.Path != after.Path {
						t.Fatal("no-op transition rewrote task")
					}
					return
				}
				for _, key := range []string{"id", "created", "updated"} {
					if after.Field(key) != before.Field(key) {
						t.Errorf("%s changed: %s", key, after.Field(key))
					}
				}
				stamp := testNow.Format(time.RFC3339Nano)
				if after.Status != to || after.Field("status_changed") != stamp {
					t.Errorf("status/timestamp: %s %s", after.Status, after.Field("status_changed"))
				}
				switch to {
				case "completed":
					if after.Field("completed_at") != stamp || field(&after.Document, "archived_at") != nil {
						t.Error("completion must set completed_at and remove archived_at")
					}
				case "archived":
					if after.Field("archived_at") != stamp {
						t.Error("archive timestamp not set")
					}
					if from == "completed" {
						if after.Field("completed_at") != before.Field("completed_at") {
							t.Error("completion timestamp not preserved")
						}
					} else if field(&after.Document, "completed_at") != nil {
						t.Error("archive from non-completed retained completed_at")
					}
					if after.Path != filepath.Join(s.Root, "archived", "2026", "10", "sample.md") {
						t.Errorf("archive path: %s", after.Path)
					}
				default:
					if field(&after.Document, "completed_at") != nil || field(&after.Document, "archived_at") != nil {
						t.Error("active status retained lifecycle timestamps")
					}
				}
				if _, err := os.Stat(before.Path); !os.IsNotExist(err) {
					t.Errorf("source remains: %v", err)
				}
			})
		}
	}
}

func TestAutomatedRequiresUnassigned(t *testing.T) {
	for _, extra := range []string{"assigned_to: agent:example\n", "assigned: example\n", "assignee: example\n", "origin: original\n"} {
		t.Run(strings.ReplaceAll(extra, "\n", "_"), func(t *testing.T) {
			s, task := seeded(t, "open", extra)
			if err := s.SetStatus(task, "automated", testNow); err == nil {
				t.Fatal("accepted assignment or clone origin")
			}
			if !bytes.Equal(task.Original, onlyTask(t, s).Original) {
				t.Fatal("rejected transition changed source")
			}
		})
	}
}

func TestRoundTripPreservesMetadata(t *testing.T) {
	extra := "# user metadata\ndescription: |\n  First line\n  Second line\ntags: [one, two]\ncustom:\n  nested: [1, true, value]\n"
	s, task := seeded(t, "open", extra)
	if err := s.SetStatus(task, "deferred", testNow); err != nil {
		t.Fatal(err)
	}
	after := onlyTask(t, s)
	for _, key := range []string{"description", "tags", "custom"} {
		var beforeValue, afterValue any
		if err := field(&task.Document, key).Decode(&beforeValue); err != nil {
			t.Fatal(err)
		}
		if err := field(&after.Document, key).Decode(&afterValue); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(beforeValue) != fmt.Sprint(afterValue) {
			t.Errorf("metadata changed: %s", key)
		}
	}
	if !bytes.Contains(after.Original, []byte("# user metadata")) {
		t.Error("comment lost")
	}
	if fi, err := os.Stat(after.Path); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("permissions not preserved: %v %v", fi, err)
	}
}

func TestAddCollisionAndMalformedSafety(t *testing.T) {
	s, original := seeded(t, "open", "")
	if err := s.Add("sample", "Duplicate", "", testNow); err == nil {
		t.Fatal("overwrote duplicate id")
	}
	if err := os.WriteFile(filepath.Join(s.Root, "deferred", "broken.md"), []byte("---\ntitle: [broken\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(original, "completed", testNow); err == nil {
		t.Fatal("wrote into malformed store")
	}
	if err := s.Remove(original); err == nil {
		t.Fatal("removed from malformed store")
	}
	data, err := os.ReadFile(original.Path)
	if err != nil || !bytes.Equal(data, original.Original) {
		t.Fatal("source changed")
	}
	broken, err := os.ReadFile(filepath.Join(s.Root, "deferred", "broken.md"))
	if err != nil || string(broken) != "---\ntitle: [broken\n---\n" {
		t.Fatal("malformed file rewritten")
	}
}

func TestStaleAndDuplicateTasks(t *testing.T) {
	s, old := seeded(t, "open", "")
	if err := os.WriteFile(old.Path, append(bytes.Clone(old.Original), []byte("description: foreign edit\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(old, "completed", testNow); err == nil {
		t.Fatal("overwrote foreign edit")
	}
	current := onlyTask(t, s)
	if err := os.WriteFile(filepath.Join(s.Root, "deferred", "copy.md"), current.Original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(current); err == nil {
		t.Fatal("removed duplicate id")
	}
}

func TestScaffoldAddRemoveAndSymlinks(t *testing.T) {
	parent := t.TempDir()
	if _, err := Load(parent); !os.IsNotExist(err) {
		t.Fatalf("load created missing location: %v", err)
	}
	s, err := Scaffold(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range Statuses {
		if fi, err := os.Stat(filepath.Join(s.Root, status)); err != nil || !fi.IsDir() {
			t.Fatalf("missing status directory: %s", status)
		}
	}
	if err := s.Add("new-task", "New task", "two\nlines", testNow); err != nil {
		t.Fatal(err)
	}
	task := onlyTask(t, s)
	if task.Field("description") != "two\nlines" {
		t.Fatal("description lost")
	}
	if err := s.Remove(task); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "open")); err != nil {
		t.Fatal("remove deleted directory")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(s.Root, "open", "external.md")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("unsafe", "Unsafe", "", testNow); err == nil {
		t.Fatal("wrote despite unsafe symlink")
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, fm := range []string{"", "[]", fixture("unknown"), strings.Replace(fixture("open"), "version: 1", "version: 2", 1), fixture("open") + "title: duplicate\n", strings.Replace(fixture("open"), "id: sample", "id: ../outside", 1), fixture("archived"), fixture("completed"), strings.Replace(fixture("open"), "2026-01-01T00:00:00Z", "2026-01-01", 1), strings.Replace(fixture("open"), "2026-01-01T00:00:00Z", "invalid", 1)} {
		data := "---\n" + fm + "---\nbody"
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("accepted malformed task: %q", data)
		}
	}
}
