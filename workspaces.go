package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

type workspace struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

// The feed contains a name, never a root. Only workspace.list resolves that name.
func listWorkspaces() ([]workspace, error) {
	addr := os.Getenv("AUTODOC_SOCKET")
	if addr == "" {
		return nil, errors.New("AUTODOC_SOCKET unavailable; browse an external location")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	c, err := golibrpc.Dial(ctx, addr, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	// workspace.list belongs to the stable protocol-13 surface (AutoDoc SDK v0.1.21).
	if _, err := c.Call(ctx, "sys.hello", map[string]any{"protocol": 13, "name": "autodoc-todos"}); err != nil {
		return nil, err
	}
	v, err := c.Call(ctx, "workspace.list")
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var all []workspace
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	var usable []workspace
	for _, w := range all {
		if w.Name != "" && filepath.IsAbs(w.Root) {
			usable = append(usable, w)
		}
	}
	return usable, nil
}
