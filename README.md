# AutoDoc Todos

A standalone Go executable using AutoDoc's **protocol-2 dialog SDK**. It reads
and edits schema-1 Markdown tasks with YAML frontmatter directly on the local filesystem; it
does not depend on Neovim, Lua, auto-core, auto-finder, or their mailbox runtime.

## Build and install

From this directory:

```sh
go build -buildvcs=false -trimpath -o bin/autodoc-todos .
go test -race ./...
go vet ./...
```

For this uncommitted local scaffold, copy the built directory into AutoDoc's
plugins folder as a real subdirectory, normally `~/.config/autodoc/plugins/todos`
(or the configured plugins folder), then reopen the Plugins menu. Symlinked
plugin subdirectories are not discovered. No git initialization is required.
Once published in a git repository, Plugins → Add from a git URL can clone and
build it. `plugin.toml` declares the build command, dialog placements, optional
document feed, and Load/New/Refresh commands. The executable is
`bin/autodoc-todos`; stdout is reserved for the SDK's msgpack-RPC transport.
Minimum useful dialog size is 48 × 12. Requires AutoDoc v0.1.21+ and Go 1.25.3+.
`-buildvcs=false` supports this intentionally uninitialized local source directory.

## Locations

The opening picker has recent locations and workspaces on the left, and a
normal OS directory browser on the right. `.todo-list` directories and parents
containing them are highlighted with `*`. Hidden directories are included;
directory symlinks are omitted from browsing to avoid cycles.

- **New** scaffolds `.todo-list` and all six status directories in a selected,
  existing parent. It never creates arbitrary ancestor trees or overwrites tasks.
- **Load** opens an existing store, without scaffolding it.
- **Close** unloads the location; it never deletes the directory or tasks.

Both a containing directory and `.todo-list` itself are accepted. External
locations are first-class and remain loaded when the host changes documents or
workspaces. The document feed supplies a workspace **name, not a root**. Roots
are discovered through `workspace.list` on `AUTODOC_SOCKET` (API protocol 13,
supported by AutoDoc SDK v0.1.21 and compatible newer daemons). If discovery is
unavailable, external browsing still works; no workspace name is treated as a
filesystem path. Recent locations are atomically saved under
`$XDG_STATE_HOME/autodoc-todos/locations.json`, defaulting to
`~/.local/state/autodoc-todos/locations.json`.

## Keys

| Key | Action |
| --- | --- |
| `CR` / Enter | Toggle a tree header; preview a task's Markdown inside the dialog |
| `i` | Preview / return to tree |
| `a` | Add title, then optional description; Enter saves |
| `d` | Request removal; only `y` confirms, `n` cancels |
| `s` | Choose status with `j`/`k` or `1`–`6`; Enter applies |
| `o` | Toggle the selected group |
| `O` | Expand/collapse all, including archive years and months |
| `R` | Refresh from disk |
| `M` | Location picker |
| `?` | Scrollable help and store diagnostics |
| `A` | Assignment unavailable; explanatory message only |
| `e` | Vars editing unavailable; explanatory message only |
| `N` / `L` / `C` | New picker / Load picker / unload |
| Tab | Switch top actions/tree or picker panes |
| Arrows / `j` / `k` | Select; `h`/`l` select top actions |
| PageUp / PageDown | Scroll tree, preview, or help |
| Ctrl+g | Cancel form or confirmation; return from help |
| Backspace | Return from preview; in picker browse parent |
| Esc | Host hides the dialog; loaded state is retained |
| `q` | Ask the host to close the plugin |

Picker: Enter browses a directory, or loads a selected `.todo-list` / left-pane
location. Space selects the highlighted location; `l` loads the current browser
directory, `n` scaffolds there, `/` accepts a typed path (including `~/...`).
In the description form, Ctrl+j or Shift+Enter inserts a newline; Backspace
removes one Unicode character.

**Host navigation limitation:** AutoDoc's plugin protocol has no editor-open /
navigation verb. `CR` therefore displays an honest in-plugin Markdown preview,
not a host editor jump. Assignment (`A`) and Vars (`e`) require the owning
Neovim runtime and are intentionally not emulated here. Existing assignment,
variable, automation, description, and unknown metadata fields are preserved.

## Portable format and status compatibility

Each task has `id`, integer `version: 1`, `status`, `title`, `created`, `updated`,
and `status_changed`. New tasks use UTC RFC3339 timestamps and safe, collision-
resistant IDs. Lifecycle timestamps must be RFC3339 with an offset (fractional
seconds allowed); date-only lifecycle timestamps are rejected. `due` is a bare
`YYYY-MM-DD` date. Version must be integer 1. The filename safety gate is
`^[A-Za-z0-9][A-Za-z0-9_-]*$`; the inspected core schema only requires a string
ID, so this adapter deliberately imposes an additional filesystem safety gate.

```text
.todo-list/
  open/<id>.md
  in-progress/<id>.md
  automated/<id>.md
  deferred/<id>.md
  completed/<id>.md
  archived/YYYY/MM/<id>.md
```

Only `.md` tasks in these bucket locations are scanned, matching core's flat
buckets and two-level archive walk. Unrelated `.yaml` / `.yml` files are ignored;
there is no legacy YAML import mode or automatic migration.

Files open with `---`, contain a YAML frontmatter mapping, then close it with
another `---` line. Everything below that delimiter is the Markdown body.
Description comes from that body, never a frontmatter `description` field.
For decoded description display, the adapter strips the canonical managed
comment and one leading H1, then trims structural blank lines, like core's
`md.decode`. The source body itself remains byte-exact on status moves.
New tasks put description in the body and include a short instructional managed
marker (recognized by core's decoder) and title H1; they never serialize
description into frontmatter. Existing markers are not duplicated, and existing
bodies are not rewritten to add a marker.

Status transitions preserve `id`, `created`, and `updated`; only
`status_changed` is bumped. `updated` belongs to content edits, not transitions.

- Entering `open`, `in-progress`, `automated`, or `deferred` clears both
  `completed_at` and `archived_at`.
- Entering `completed` sets `completed_at` and clears `archived_at`.
- Entering `archived` sets `archived_at`; `completed_at` is retained **only**
  when arriving from `completed`. Archive placement and the tree's year/month
  grouping derive from `archived_at`.
- Selecting the existing status is a no-op, preserving bytes and timestamps.
- Entering `automated` requires an unassigned task without `origin`. Populated
  `assigned_to`, `assigned`, or `assignee` fields block it; ambiguous assignment
  aliases also block it. The current core schema permits empty inert templates,
  so no `condition` or `execute` is synthesized. Nothing is evaluated or executed.
- `condition` / `execute` must be string lists when supplied and are valid only
  on automated tasks, as is `last_fired_at`. Leaving a populated template is
  refused until those fields are removed externally rather than silently
  discarding its metadata. Empty inert templates can transition normally.

The tree always has six status headings, with archived tasks nested under
year/month headings. Malformed Markdown/frontmatter is reported, not silently discarded or
rewritten. Existing stores can be inspected with issues present, but writes
are blocked until those issues are repaired externally.

## Safety and standalone boundaries

- Status moves patch only changed lifecycle scalars and append/remove lifecycle
  fields as needed. Untouched YAML nodes, formatting, comments, and the entire
  Markdown body retain their byte contents, including CRLF. Inline comments on
  removed lifecycle fields survive as comment lines. Anchored or multiline
  managed scalars are refused rather than corrupting metadata references.
- Known field shapes and lifecycle rules are checked. Unknown frontmatter
  fields are deliberately preserved, not deleted: core's closed schema rejects
  such extensions, so preserving them does **not** claim schema acceptance.
  Similarly, Go accepts broader YAML syntax than core's strict-subset parser;
  preserved arbitrary YAML is not guaranteed consumable by core. RFC3339 parsing
  is stricter than core's datetime-prefix check. New tasks and canonical fixtures
  are verified against the actual Lua decoder/schema, not a mock.
- Temporary-file writes are synced and atomically published. New destinations
  use an exclusive hard link, refusing collisions; source permissions survive
  status moves. Filesystems must support same-directory hard links.
- Duplicate IDs, malformed files, and symlinks block writes. Byte comparisons
  refuse stale edits. Removal deletes exactly one unchanged task file, never
  a containing directory, and the UI requires explicit confirmation.
- `.autodoc-todos.lock` serializes this plugin's writers. A crashed writer can
  leave a lock: inspect and remove that exact lock externally only after
  confirming no plugin writer is active.
- Atomic publication is **not** a transaction with foreign writers, nor is a
  cross-directory move one atomic operation. Do not edit the same store in
  another runtime concurrently. If publication succeeds but source removal
  fails or detects an edit, both files are retained and an error is shown for
  manual recovery. This avoids silently deleting a foreign edit.
- No task events, mailbox notifications, assignment side effects, automation,
  host editor navigation, or host todo-panel refresh are generated. Reload
  the owning runtime after standalone edits to see them there.

`store/` is deliberately a pure-Go **portable-format adapter**, not a port of
the Vim-dependent Lua runtime. A future shared implementation could extract a
pure Lua format/state-machine module, separate from editor, mailbox, and event
dependencies, and expose it to independent consumers. This plugin neither
imports nor modifies that runtime.

## Coverage

Tests cover all 36 status pairs, timestamp cleanup/preservation, automated
eligibility, metadata round trips, malformed files, duplicate IDs, stale
writes, destination collisions, symlink guards, scaffolding, removal safety,
browser highlighting, location independence, recents, tree expansion, forms,
confirmation, previews, small sizes, theme changes, help diagnostics, and the
real SDK protocol-2 lifecycle over pipes.

When Neovim and the sibling core checkout are available, tests validate Go-created
tasks in all six statuses using actual `md.decode` and `schema.validate`. Set
`AUTODOC_TODOS_CORE_RUNTIME` to override the core checkout directory. The optional
real-store parity test is strictly read-only (no lock, scaffold, move, or write):

```sh
AUTODOC_TODOS_REAL_STORE=/path/to/.todo-list go test ./store -run TestRealStoreReadOnlyParity -v -count=1
```

The reviewed real store matched both readers: **357 tasks, 286 archived, zero
malformed**, with identical IDs, statuses, and decoded descriptions. No real
store or KB files were changed.