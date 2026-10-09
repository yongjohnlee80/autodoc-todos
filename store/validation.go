package store

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Schema v1 is closed: mirror auto-core.todo.schema's FIELDS catalog.
var topLevelFields = map[string]bool{
	"id": true, "version": true, "created": true, "updated": true,
	"status_changed": true, "status": true, "completed_at": true, "archived_at": true,
	"title": true, "description": true, "due": true, "priority": true,
	"assignee": true, "tags": true, "adr": true, "review": true, "blocked": true,
	"condition": true, "execute": true, "origin": true, "last_fired_at": true,
	"exit_code": true, "errors": true,
}

func present(t *Task, key string) bool {
	n := field(&t.Document, key)
	return n != nil && n.Tag != "!!null"
}

func validateTask(t *Task) error {
	m := t.Document.Content[0]
	for i := 0; i < len(m.Content); i += 2 {
		key := m.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !topLevelFields[key.Value] {
			return fmt.Errorf("unknown top-level key %q (schema v1 is closed)", key.Value)
		}
	}
	for _, key := range []string{"id", "status", "title", "assignee", "origin"} {
		if n := field(&t.Document, key); n != nil && n.Tag != "!!null" && (n.Kind != yaml.ScalarNode || n.Tag != "!!str") {
			return fmt.Errorf("%s must be a string", key)
		}
	}
	for _, key := range []string{"tags", "adr", "review", "blocked", "condition", "execute"} {
		n := field(&t.Document, key)
		if n == nil || n.Tag == "!!null" {
			continue
		}
		if key != "condition" && key != "execute" && n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value != "" {
			continue
		}
		if n.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s must be a list of strings", key)
		}
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
				return fmt.Errorf("%s must be a list of strings", key)
			}
		}
	}
	if present(t, "priority") {
		p := t.Field("priority")
		if p != "low" && p != "normal" && p != "high" {
			return fmt.Errorf("invalid priority")
		}
	}
	if present(t, "due") && !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(t.Field("due")) {
		return fmt.Errorf("invalid due date")
	}
	if present(t, "last_fired_at") {
		if _, err := timestamp(t.Field("last_fired_at")); err != nil {
			return fmt.Errorf("invalid last_fired_at")
		}
	}
	if n := field(&t.Document, "exit_code"); n != nil && n.Tag != "!!null" && n.Tag != "!!int" {
		return fmt.Errorf("exit_code must be integer")
	}
	switch t.Status {
	case "completed":
		if !present(t, "completed_at") || present(t, "archived_at") {
			return fmt.Errorf("completed requires completed_at and no archived_at")
		}
	case "archived":
		if !present(t, "archived_at") {
			return fmt.Errorf("archived requires archived_at")
		}
	default:
		if present(t, "completed_at") || present(t, "archived_at") {
			return fmt.Errorf("active task cannot have completion/archive timestamps")
		}
	}
	if t.Status == "automated" {
		if err := automatedReady(t); err != nil {
			return err
		}
	} else {
		for _, key := range []string{"condition", "execute", "last_fired_at"} {
			if present(t, key) {
				return fmt.Errorf("%s is only valid on automated tasks", key)
			}
		}
	}
	return validateErrors(field(&t.Document, "errors"))
}

var errorCodes = map[string]bool{
	"not-found": true, "unresolved-variable": true, "automation-template-assignee": true,
	"automation-origin-not-found": true, "automation-condition-malformed": true,
	"automation-execute-malformed": true, "automation-step-failed": true,
	"automation-bash-disabled": true, "automation-bash-not-allowlisted": true,
	"automation-bash-t-no-resolver": true, "automation-bash-t-range": true,
	"automation-bash-t-slot-admin": true, "automation-slot-no-resolver": true,
}

func validateErrors(n *yaml.Node) error {
	if n == nil || n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("errors must be a list")
	}
	for _, entry := range n.Content {
		if entry.Kind != yaml.MappingNode {
			return fmt.Errorf("errors entry must be a mapping")
		}
		doc := yaml.Node{Content: []*yaml.Node{entry}}
		for _, key := range []string{"field", "code", "message", "detected"} {
			v := field(&doc, key)
			if v == nil || v.Kind != yaml.ScalarNode || (v.Tag != "!!str" && !(key == "detected" && v.Tag == "!!timestamp")) {
				return fmt.Errorf("errors.%s must be a string", key)
			}
		}
		if !errorCodes[field(&doc, "code").Value] {
			return fmt.Errorf("unknown errors.code")
		}
		if _, err := timestamp(field(&doc, "detected").Value); err != nil {
			return fmt.Errorf("invalid errors.detected")
		}
		for i := 0; i < len(entry.Content); i += 2 {
			switch entry.Content[i].Value {
			case "field", "code", "message", "detected":
			default:
				return fmt.Errorf("unknown errors entry key")
			}
		}
	}
	return nil
}
