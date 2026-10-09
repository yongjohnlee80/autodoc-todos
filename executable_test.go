package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

func TestExecutableSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bin := filepath.Join(t.TempDir(), "autodoc-todos")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	t.Setenv("AUTODOC_SOCKET", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	toPlugin, fromHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromPlugin, toHost, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bin)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = toPlugin, toHost, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = toPlugin.Close()
	_ = toHost.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	hs := &testHost{frames: make(chan []plugin.Row, 64), ready: make(chan int, 1)}
	hs.link, err = plugin.NewLink(ctx, plugin.FileConn(fromPlugin, fromHost), func(method string, params []any) {
		switch method {
		case plugin.MethodReady:
			if v, err := plugin.ReadReady(params); err == nil {
				hs.ready <- v
			}
		case plugin.MethodFrame:
			if rows, _, err := plugin.ReadFrame(params); err == nil {
				hs.frames <- rows
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); hs.link.Close() })
	hs.send(t, plugin.MethodOpen, plugin.OpenParams(plugin.Open{Protocol: 2, Width: 100, Height: 24}))
	select {
	case v := <-hs.ready:
		if v != 2 {
			t.Fatalf("executable answered protocol %d", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executable did not answer host.ready")
	}
	hs.until(t, "Recent / workspaces")
	hs.send(t, plugin.MethodClose, plugin.EmptyParams())
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executable: %v\n%s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executable did not close")
	}
}
