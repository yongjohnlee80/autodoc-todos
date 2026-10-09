// Package browser uses the local OS filesystem, independently of AutoDoc's document index.
package browser

import (
	"os"
	"path/filepath"
	"sort"
)

type Entry struct {
	Name, Path string
	Todo       bool
}

func List(path string) ([]Entry, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, dir := range dirs {
		// Avoid directory symlink cycles; a user can still type a deliberate external path.
		if !dir.IsDir() || dir.Type()&os.ModeSymlink != 0 {
			continue
		}
		p := filepath.Join(abs, dir.Name())
		todo := dir.Name() == ".todo-list"
		if fi, err := os.Lstat(filepath.Join(p, ".todo-list")); err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
			todo = true
		}
		entries = append(entries, Entry{Name: dir.Name(), Path: p, Todo: todo})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Todo != entries[j].Todo {
			return entries[i].Todo
		}
		return entries[i].Name < entries[j].Name
	})
	if parent := filepath.Dir(abs); parent != abs {
		entries = append([]Entry{{Name: "..", Path: parent}}, entries...)
	}
	return entries, nil
}
