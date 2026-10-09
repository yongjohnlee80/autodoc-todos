package main

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

func TestWorkspaceDiscoveryTransport(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(root, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(_ context.Context, req *golibrpc.Request) (any, error) {
		if len(req.Params) != 1 {
			return nil, fmt.Errorf("hello requires parameters")
		}
		m, _ := req.Params[0].(map[string]any)
		if m["protocol"] != int64(13) || m["name"] != "autodoc-todos" {
			return nil, fmt.Errorf("wrong declaration: %v", m)
		}
		req.Session.SetValue("hello", true)
		return map[string]any{"server": "autodoc", "protocol": int64(13)}, nil
	})
	srv.Handle("workspace.list", func(_ context.Context, req *golibrpc.Request) (any, error) {
		if req.Session.Value("hello") != true || len(req.Params) != 0 {
			return nil, fmt.Errorf("handshake or parameter mismatch")
		}
		return []any{
			map[string]any{"name": "name-not-path", "root": root, "state": "ready", "extra": true},
			map[string]any{"name": "relative", "root": "not-absolute"},
			map[string]any{"name": "", "root": root},
		}, nil
	})
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv("AUTODOC_SOCKET", sock)
	all, err := listWorkspaces()
	if err != nil || len(all) != 1 || all[0].Name != "name-not-path" || all[0].Root != root {
		t.Fatalf("workspace.list: %+v %v", all, err)
	}
}

func TestWorkspaceDiscoveryUnavailable(t *testing.T) {
	t.Setenv("AUTODOC_SOCKET", "")
	if _, err := listWorkspaces(); err == nil {
		t.Fatal("missing socket not reported")
	}
	t.Setenv("AUTODOC_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	if _, err := listWorkspaces(); err == nil {
		t.Fatal("missing daemon not reported")
	}
}
