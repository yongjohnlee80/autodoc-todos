package store

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var unknownFields = []struct {
	key, value string
}{
	{"custom", "value"},
	{"custom", "{nested: [1, true, value]}"},
	{"custom", "null"},
	{"assigned_to", "agent:example"},
	{"assigned", "example"},
	{"wip", "worktree"},
	{"pr", "123"},
	{"links", "[doc]"},
	{"notes", "scratch"},
	{"Status", "open"},
}

func TestParseRejectsUnknownTopLevelFields(t *testing.T) {
	for _, tc := range unknownFields {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			_, err := Parse([]byte(markdownFixture("open", tc.key+": "+tc.value+"\n")))
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("expected unknown field %q diagnostic, got %v", tc.key, err)
			}
		})
	}
}

func storeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestUnknownTopLevelFieldsBlockMutations(t *testing.T) {
	for _, op := range []string{"add", "status", "remove"} {
		t.Run(op, func(t *testing.T) {
			s, task := seeded(t, "open", "")
			path := filepath.Join(s.Root, "deferred", "bad.md")
			data := []byte(strings.Replace(markdownFixture("deferred", "custom: {nested: [value]} # keep\n"), "id: sample", "id: bad", 1))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			snap, err := s.Scan()
			if err != nil {
				t.Fatal(err)
			}
			if len(snap.Tasks) != 1 || len(snap.Issues) != 1 || !strings.Contains(strings.Join(snap.Issues, "\n"), "custom") || !strings.Contains(strings.Join(snap.Issues, "\n"), path) {
				t.Errorf("expected valid task and unknown-field scan issue: %+v", snap)
			}
			before := storeBytes(t, s.Root)
			switch op {
			case "add":
				err = s.Add("new", "New", "Body", testNow)
			case "status":
				err = s.SetStatus(task, "completed", testNow)
			case "remove":
				err = s.Remove(task)
			}
			if err == nil || !strings.Contains(err.Error(), "custom") {
				t.Errorf("expected mutation refusal with field diagnostic, got %v", err)
			}
			if !reflect.DeepEqual(before, storeBytes(t, s.Root)) {
				t.Error("refused mutation changed store file set or bytes")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("malformed task bytes changed")
			}
		})
	}
}
