//go:build unix

package workspacetools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-tools/fileops"
	"github.com/mattsp1290/eino-tools/result"
)

func TestMountedReaderRejectsFIFOsAndStillReadsRegularFiles(t *testing.T) {
	ctx := context.Background()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(workspace, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pipe", filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "regular"), []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := composition.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	mount, err := Mount(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer mount.Close(ctx)
	plan, err := registry.AcquireRunPlan(ctx, runtime.RunPlanRequest{Config: structConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Release()
	tools, err := plan.ResolveTools(ctx, runtime.ToolScopeContext{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	var reader runtime.Tool
	for _, tool := range tools {
		if tool.Name == "file_read" {
			reader = tool
		}
	}
	if reader.Executor == nil {
		t.Fatal("file_read was not mounted")
	}
	execute := func(args fileops.ReadArgs) fileops.ReadResult {
		t.Helper()
		input, marshalErr := json.Marshal(args)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		done := make(chan struct{})
		var raw runtime.ToolResult
		var executeErr error
		go func() {
			defer close(done)
			raw, executeErr = reader.Executor.Execute(ctx, runtime.ToolCall{Name: "file_read", Input: input, Context: runtime.ToolContext{WorkspaceRoot: workspace}})
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			// Release a regressed blocking reader before failing the test.
			fd, openErr := syscall.Open(fifo, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
			if openErr == nil {
				_ = syscall.Close(fd)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("blocked reader did not exit after releasing FIFO")
			}
			t.Fatal("mounted file_read blocked on a FIFO")
		}
		var decoded fileops.ReadResult
		if executeErr != nil || json.Unmarshal(raw.Structured, &decoded) != nil {
			t.Fatalf("read result invalid: %v", executeErr)
		}
		return decoded
	}
	for _, path := range []string{"pipe", "link"} {
		for _, window := range []bool{false, true} {
			args := fileops.ReadArgs{Path: path}
			if window {
				offset, limit := 2, 1
				args.Offset, args.Limit = &offset, &limit
			}
			if got := execute(args); got.Outcome != result.OutcomeFailed || got.Error == nil {
				t.Fatalf("FIFO was not rejected: %+v", got)
			}
			args.Path = "regular"
			want := "first\nsecond\n"
			if window {
				want = "second\n"
			}
			if got := execute(args); got.Outcome != result.OutcomeSucceeded || got.Content != want {
				t.Fatalf("read after FIFO rejection: %+v", got)
			}
		}
	}
}
