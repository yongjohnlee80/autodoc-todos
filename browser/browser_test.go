package browser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListHighlightsStoresAndSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"plain", "has-todos/.todo-list", ".todo-list", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.yaml"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "plain"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	entries, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"..", ".todo-list", "has-todos", ".hidden", "plain"}
	if len(entries) != len(want) {
		t.Fatalf("entries: %+v", entries)
	}
	for i, e := range entries {
		if e.Name != want[i] || !filepath.IsAbs(e.Path) || e.Todo != (i == 1 || i == 2) {
			t.Errorf("entry %d: %+v", i, e)
		}
	}
	if _, err := List(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Errorf("missing directory: %v", err)
	}
}
