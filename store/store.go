// Package store is the standalone schema-1 portable-format adapter. It never runs automation.
package store

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var Statuses = []string{"open", "in-progress", "automated", "deferred", "completed", "archived"}
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type Task struct {
	ID, Status, Title, Path string
	Document                yaml.Node
	Original                []byte
	Body                    string
}

func (t *Task) Field(key string) string {
	if key == "description" {
		return cleanBody(t.Body)
	}
	if n := field(&t.Document, key); n != nil && n.Tag != "!!null" {
		return n.Value
	}
	return ""
}

func field(doc *yaml.Node, key string) *yaml.Node {
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	m := doc.Content[0]
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func set(doc *yaml.Node, key, value, tag string) {
	if n := field(doc, key); n != nil {
		n.Kind, n.Tag, n.Value = yaml.ScalarNode, tag, value
		n.Content, n.Alias = nil, nil
		return
	}
	m := doc.Content[0]
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
}

func unset(doc *yaml.Node, key string) {
	m := doc.Content[0]
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func nonempty(n *yaml.Node) bool {
	if n == nil || n.Tag == "!!null" {
		return false
	}
	if n.Kind == yaml.AliasNode {
		// Avoid cycles and uncertain template shapes in standalone validation.
		return false
	}
	if n.Kind == yaml.ScalarNode {
		return strings.TrimSpace(n.Value) != ""
	}
	return len(n.Content) != 0
}

func automatedReady(t *Task) error {
	if n := field(&t.Document, "origin"); n != nil && n.Tag != "!!null" {
		return errors.New("automated templates cannot have origin")
	}
	for _, key := range []string{"assigned_to", "assigned", "assignee"} {
		n := field(&t.Document, key)
		if nonempty(n) || (n != nil && n.Kind == yaml.AliasNode) {
			return errors.New("automated requires an unassigned task; assignment is managed externally")
		}
	}
	return nil
}

func Parse(data []byte) (*Task, error) {
	fm, body, _, _, err := splitMarkdown(data)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(fm))
	if err := d.Decode(&doc); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("expected one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("expected a YAML mapping")
	}
	// Decode also validates duplicate keys and aliases without discarding the original nodes.
	var check map[string]any
	if err := doc.Decode(&check); err != nil {
		return nil, err
	}
	for _, key := range []string{"id", "version", "status", "title", "created", "updated", "status_changed"} {
		n := field(&doc, key)
		if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" || (key != "title" && strings.TrimSpace(n.Value) == "") {
			return nil, fmt.Errorf("missing or non-scalar %s", key)
		}
	}
	t := &Task{Document: doc, Original: bytes.Clone(data), Body: string(body)}
	t.ID, t.Status, t.Title = t.Field("id"), t.Field("status"), t.Field("title")
	if !validID.MatchString(t.ID) {
		return nil, errors.New("id must be a safe filename (letters, digits, - or _)")
	}
	if n := field(&doc, "version"); n.Tag != "!!int" || n.Value != "1" {
		return nil, errors.New("unsupported schema version (expected integer 1)")
	}
	if !ValidStatus(t.Status) {
		return nil, fmt.Errorf("unknown status %q", t.Status)
	}
	for _, key := range []string{"created", "updated", "status_changed", "completed_at", "archived_at"} {
		if n := field(&doc, key); n != nil {
			if n.Tag == "!!null" && (key == "completed_at" || key == "archived_at") {
				continue
			}
			if n.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("invalid %s", key)
			}
			if _, err := timestamp(n.Value); err != nil {
				return nil, fmt.Errorf("invalid %s: %w", key, err)
			}
		}
	}
	if err := validateTask(t); err != nil {
		return nil, err
	}
	return t, nil
}

func timestamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

func ValidStatus(s string) bool {
	for _, status := range Statuses {
		if s == status {
			return true
		}
	}
	return false
}

func (t *Task) Encode() ([]byte, error) {
	if len(t.Original) != 0 {
		return t.patchMarkdown()
	}
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(&t.Document); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	body := t.Body
	if !strings.Contains(body, "<!-- ─── auto-core.todo schema v1") {
		body = managedMarker + "\n\n" + body
	}
	return []byte("---\n" + b.String() + "---\n\n" + body), nil
}

type Store struct{ Root string }

type Snapshot struct {
	Tasks  []*Task
	Issues []string
}

// Location accepts either the containing directory or .todo-list itself. Load never creates it.
func Location(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		if strings.HasSuffix(path, string(filepath.Separator)+"~") {
			path = home
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if filepath.Base(abs) != ".todo-list" {
		abs = filepath.Join(abs, ".todo-list")
	}
	return filepath.Clean(abs), nil
}

func Load(path string) (*Store, error) {
	root, err := Location(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New(".todo-list must be a real directory, not a symlink")
	}
	return &Store{Root: root}, nil
}

func Scaffold(path string) (*Store, error) {
	root, err := Location(path)
	if err != nil {
		return nil, err
	}
	// The user chooses an existing parent; never create arbitrary ancestor trees.
	if fi, err := os.Stat(filepath.Dir(root)); err != nil || !fi.IsDir() {
		return nil, errors.New("choose an existing parent directory")
	}
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	s, err := Load(root)
	if err != nil {
		return nil, err
	}
	for _, status := range Statuses {
		if err := s.ensureDir(status); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Scan() (Snapshot, error) {
	var snap Snapshot
	if err := s.checkRoot(); err != nil {
		return snap, err
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(s.Root, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.Root, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if de.Type()&os.ModeSymlink != 0 {
			if ValidStatus(parts[0]) && ((len(parts) == 1 || parts[0] == "archived" && len(parts) <= 3) || filepath.Ext(path) == ".md") {
				snap.Issues = append(snap.Issues, path+": symlink is not supported")
			}
			return nil
		}
		if de.IsDir() {
			if rel == "." {
				return nil
			}
			if !ValidStatus(parts[0]) || (parts[0] != "archived" && len(parts) > 1) || len(parts) > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" || !ValidStatus(parts[0]) || (parts[0] == "archived" && len(parts) != 4) || (parts[0] != "archived" && len(parts) != 2) {
			return nil
		}
		if !de.Type().IsRegular() {
			snap.Issues = append(snap.Issues, path+": not a regular file")
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		t, err := Parse(data)
		if err != nil {
			snap.Issues = append(snap.Issues, path+": "+err.Error())
			return nil
		}
		t.Path = path
		if seen[t.ID] {
			snap.Issues = append(snap.Issues, "duplicate id: "+t.ID)
		}
		seen[t.ID] = true
		snap.Tasks = append(snap.Tasks, t)
		return nil
	})
	sort.Slice(snap.Tasks, func(i, j int) bool { return snap.Tasks[i].ID < snap.Tasks[j].ID })
	return snap, err
}

func (s *Store) checkRoot() error {
	if fi, err := os.Lstat(s.Root); err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("store root is missing or unsafe")
	}
	return nil
}

func (s *Store) ensureDir(rel string) error {
	path := s.Root
	if err := s.checkRoot(); err != nil {
		return err
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return errors.New("unsafe directory")
		}
		path = filepath.Join(path, part)
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if fi, err := os.Lstat(path); err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("store directory is not a real directory")
		}
	}
	return nil
}

// withLock serializes standalone writers. Foreign writers are checked by byte comparison too.
func (s *Store) withLock(fn func(Snapshot) error) error {
	if err := s.checkRoot(); err != nil {
		return err
	}
	lock := filepath.Join(s.Root, ".autodoc-todos.lock")
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("store busy (or stale lock; inspect %s): %w", lock, err)
	}
	_ = f.Close()
	defer os.Remove(lock)
	snap, err := s.Scan()
	if err != nil {
		return err
	}
	if len(snap.Issues) != 0 {
		return fmt.Errorf("store has malformed, duplicate or unsafe files; repair externally before writing: %s", snap.Issues[0])
	}
	return fn(snap)
}

func NewID(title string, now time.Time) (string, error) {
	slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(title), "-"), "-")
	if slug == "" {
		slug = "task"
	}
	if len(slug) > 60 {
		slug = strings.TrimRight(slug[:60], "-")
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%x", now.Format("2006-01-02"), slug, suffix), nil
}

func (s *Store) Add(id, title, description string, now time.Time) error {
	if !validID.MatchString(id) || strings.TrimSpace(title) == "" {
		return errors.New("safe id and non-empty title required")
	}
	return s.withLock(func(snap Snapshot) error {
		for _, t := range snap.Tasks {
			if t.ID == id {
				return fmt.Errorf("id %s already exists", id)
			}
		}
		t := &Task{Document: yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}}
		stamp := now.UTC().Format(time.RFC3339Nano)
		for _, kv := range [][2]string{{"id", id}, {"status", "open"}, {"title", title}, {"created", stamp}, {"updated", stamp}, {"status_changed", stamp}} {
			set(&t.Document, kv[0], kv[1], "!!str")
		}
		set(&t.Document, "version", "1", "!!int")
		t.Body = "# " + strings.ReplaceAll(title, "\n", " ") + "\n\n" + description + "\n"
		data, err := t.Encode()
		if err != nil {
			return err
		}
		if err := s.ensureDir("open"); err != nil {
			return err
		}
		return atomicWrite(filepath.Join(s.Root, "open", id+".md"), data, 0o600, false)
	})
}

func (s *Store) current(want *Task, snap Snapshot) (*Task, error) {
	for _, t := range snap.Tasks {
		if t.Path == want.Path && t.ID == want.ID && bytes.Equal(t.Original, want.Original) {
			return t, nil
		}
	}
	return nil, errors.New("task changed or disappeared; refresh before writing")
}

func (s *Store) SetStatus(want *Task, status string, now time.Time) error {
	if !ValidStatus(status) {
		return errors.New("invalid status")
	}
	return s.withLock(func(snap Snapshot) error {
		t, err := s.current(want, snap)
		if err != nil {
			return err
		}
		if t.Status == status {
			return nil
		}
		if status == "automated" {
			if err := automatedReady(t); err != nil {
				return err
			}
		}
		if t.Status == "automated" {
			for _, key := range []string{"condition", "execute", "last_fired_at"} {
				if n := field(&t.Document, key); n != nil && n.Tag != "!!null" {
					return errors.New("remove template-only fields externally before leaving automated; metadata will not be discarded")
				}
			}
		}
		for _, key := range []string{"status", "status_changed", "completed_at", "archived_at"} {
			if n := field(&t.Document, key); n != nil && n.Anchor != "" {
				return fmt.Errorf("%s has a YAML anchor; detach it externally before changing status to preserve metadata", key)
			}
		}
		stamp := now.UTC().Format(time.RFC3339Nano)
		set(&t.Document, "status_changed", stamp, "!!str")
		set(&t.Document, "status", status, "!!str")
		rel := status
		switch status {
		case "completed":
			set(&t.Document, "completed_at", stamp, "!!str")
			unset(&t.Document, "archived_at")
		case "archived":
			if t.Status != "completed" {
				unset(&t.Document, "completed_at")
			}
			set(&t.Document, "archived_at", stamp, "!!str")
			at, _ := timestamp(t.Field("archived_at"))
			rel = filepath.Join(status, at.Format("2006"), at.Format("01"))
		default:
			unset(&t.Document, "completed_at")
			unset(&t.Document, "archived_at")
		}
		if err := s.ensureDir(rel); err != nil {
			return err
		}
		dest := filepath.Join(s.Root, rel, t.ID+filepath.Ext(t.Path))
		data, err := t.Encode()
		if err != nil {
			return err
		}
		if _, err := Parse(data); err != nil {
			return fmt.Errorf("refusing invalid transition output: %w", err)
		}
		fi, err := os.Stat(t.Path)
		if err != nil {
			return err
		}
		if err := unchanged(t); err != nil {
			return err
		}
		if err := atomicWrite(dest, data, fi.Mode().Perm(), dest == t.Path); err != nil {
			return err
		}
		if dest != t.Path {
			if err := unchanged(t); err != nil {
				// Keep both versions for manual recovery instead of deleting a foreign edit.
				return fmt.Errorf("destination saved but source changed; both kept: %w", err)
			}
			if err := os.Remove(t.Path); err != nil {
				return fmt.Errorf("destination saved; source removal failed (both kept): %w", err)
			}
			syncDir(filepath.Dir(t.Path))
		}
		return nil
	})
}

func unchanged(t *Task) error {
	fi, err := os.Lstat(t.Path)
	if err != nil || !fi.Mode().IsRegular() {
		return errors.New("task is missing or no longer a regular file")
	}
	data, err := os.ReadFile(t.Path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, t.Original) {
		return errors.New("task changed; refresh before writing")
	}
	return nil
}

// Remove deletes exactly one unchanged regular task file, never its containing directories.
func (s *Store) Remove(want *Task) error {
	return s.withLock(func(snap Snapshot) error {
		t, err := s.current(want, snap)
		if err != nil {
			return err
		}
		if err := unchanged(t); err != nil {
			return err
		}
		if err := os.Remove(t.Path); err != nil {
			return err
		}
		syncDir(filepath.Dir(t.Path))
		return nil
	})
}

func atomicWrite(path string, data []byte, mode os.FileMode, replace bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".autodoc-todos-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if replace {
		err = os.Rename(f.Name(), path)
	} else {
		// Publishing a completed temporary inode is atomic and refuses an existing destination.
		err = os.Link(f.Name(), path)
	}
	if err == nil {
		syncDir(filepath.Dir(path))
	}
	return err
}

func syncDir(path string) {
	if f, err := os.Open(path); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}
