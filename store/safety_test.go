package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStatusRefusesMetadataAliasesToManagedFields(t *testing.T) {
	s, task := seeded(t, "open", "custom_status: &state open\ncustom_alias: *state\n")
	data := bytes.Replace(task.Original, []byte("status: open"), []byte("status: &status open"), 1)
	data = bytes.Replace(data, []byte("custom_status: &state open"), []byte("custom_status: *status"), 1)
	data = bytes.Replace(data, []byte("custom_alias: *state\n"), nil, 1)
	if err := os.WriteFile(task.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	task = onlyTask(t, s)
	if err := s.SetStatus(task, "deferred", testNow); err == nil {
		t.Fatal("removing an anchored field can corrupt unknown metadata")
	}
	if !bytes.Equal(task.Original, onlyTask(t, s).Original) {
		t.Fatal("unsafe alias document rewritten")
	}
}

func TestAutomatedRefusesAliasAssignment(t *testing.T) {
	s, task := seeded(t, "open", "owner: &owner example\nassigned_to: *owner\n")
	if err := s.SetStatus(task, "automated", testNow); err == nil {
		t.Fatal("alias bypassed assignment guard")
	}
}

func TestUnsafeRootRefusesWrites(t *testing.T) {
	s, task := seeded(t, "open", "")
	oldRoot := s.Root + "-original"
	if err := os.Rename(s.Root, oldRoot); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.Symlink(external, s.Root); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(task, "deferred", testNow); err == nil {
		t.Fatal("write accepted replaced symlink root")
	}
	if _, err := s.Scan(); err == nil {
		t.Fatal("scan accepted replaced symlink root")
	}
	data, err := os.ReadFile(filepath.Join(oldRoot, "open", "sample.md"))
	if err != nil || !bytes.Equal(data, task.Original) {
		t.Fatal("original task changed")
	}
	entries, err := os.ReadDir(external)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote through unsafe store root")
	}
}

func TestDestinationCollisionDoesNotOverwrite(t *testing.T) {
	s, task := seeded(t, "open", "")
	dest := filepath.Join(s.Root, "deferred", "sample.md")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(task, "deferred", testNow); err == nil {
		t.Fatal("overwrote existing destination")
	}
	data, err := os.ReadFile(task.Path)
	if err != nil || !bytes.Equal(data, task.Original) {
		t.Fatal("source changed after collision")
	}
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		t.Fatal("destination changed")
	}
}
