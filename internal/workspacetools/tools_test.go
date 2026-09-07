package workspacetools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/config"
	"github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
)

func TestEnabledNamesAndConfigAreIsolated(t *testing.T) {
	want := []string{"file_read", "file_list", "glob", "search"}
	first := EnabledNames()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("names=%v", first)
	}
	first[0] = "shell"
	cfg := Config()
	if cfg.Enabled == nil || !reflect.DeepEqual(cfg.Enabled, want) || len(cfg.Disabled) != 0 || len(cfg.Permissions) != 0 {
		t.Fatalf("config=%#v", cfg)
	}
	cfg.Enabled[0] = "file_write"
	if !reflect.DeepEqual(Config().Enabled, want) {
		t.Fatal("configuration shares mutable allowlist state")
	}
}

func TestMountedSearchExecutesWithMinimalEnvironment(t *testing.T) {
	ctx := context.Background()
	workspace := filepath.Join(t.TempDir(), "workspace with space-λ")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "fixture.txt"), []byte("distinct-search-needle\n"), 0o600); err != nil {
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
	for _, tool := range tools {
		if tool.Name != "search" {
			continue
		}
		result, err := tool.Executor.Execute(ctx, runtime.ToolCall{
			Name: "search", Input: json.RawMessage(`{"pattern":"distinct-search-needle","path":"."}`),
			Context: runtime.ToolContext{WorkspaceRoot: workspace},
		})
		if err != nil || !strings.Contains(string(result.Structured), "fixture.txt") {
			t.Fatalf("search failed without inherited environment: result bytes=%d err=%v", len(result.Structured), err)
		}
		return
	}
	t.Fatal("search tool was not resolved")
}

func TestMountSelectsOnlyReadOnlyToolsAndHasStableFingerprint(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	build := func() session.ExtensionPlanDescriptor {
		registry, err := composition.NewRegistry(nil)
		if err != nil {
			t.Fatal(err)
		}
		mount, err := Mount(ctx, registry)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { mount.Deactivate(); _ = mount.Close(context.Background()) })
		plan, err := registry.AcquireRunPlan(ctx, runtime.RunPlanRequest{Config: structConfig()})
		if err != nil {
			t.Fatal(err)
		}
		defer plan.Release()
		resolved, err := plan.ResolveTools(ctx, runtime.ToolScopeContext{WorkspaceRoot: workspace})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(resolved))
		for i := range resolved {
			got[i] = resolved[i].Name
		}
		if !reflect.DeepEqual(got, EnabledNames()) {
			t.Fatalf("resolved=%v", got)
		}
		return plan.Descriptor()
	}
	first := build()
	t.Setenv("EINO_TUI_UNRELATED_IDENTITY_TEST", "changed")
	second := build()
	if first.Fingerprint == "" || first.Fingerprint != second.Fingerprint {
		t.Fatalf("descriptors=%#v %#v", first, second)
	}
}

func TestExecutableIdentityAndFailures(t *testing.T) {
	ctx := context.Background()
	makeExecutable := func(name, body string) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	rgA := makeExecutable("rg", "#!/bin/sh\nexit 0\n")
	rgB := makeExecutable("rg", "#!/bin/sh\nexit 1\n")
	rgMoved := makeExecutable("rg", "#!/bin/sh\nexit 0\n")
	sh := makeExecutable("sh", "#!/bin/sh\nexit 0\n")
	fingerprint := func(rg string) string {
		registry, _ := composition.NewRegistry(nil)
		mount, err := mountWithExecutables(ctx, registry, rg, sh)
		if err != nil {
			t.Fatal(err)
		}
		defer mount.Close(ctx)
		plan, err := registry.AcquireRunPlan(ctx, runtime.RunPlanRequest{Config: structConfig()})
		if err != nil {
			t.Fatal(err)
		}
		defer plan.Release()
		return plan.Descriptor().Fingerprint
	}
	if fingerprint(rgA) == fingerprint(rgB) {
		t.Fatal("changed ripgrep bytes did not change plan identity")
	}
	if fingerprint(rgA) == fingerprint(rgMoved) {
		t.Fatal("changed ripgrep invocation path did not change plan identity")
	}
	registry, _ := composition.NewRegistry(nil)
	if _, err := mountWithExecutables(ctx, registry, filepath.Join(t.TempDir(), "missing-rg"), sh); err == nil {
		t.Fatal("missing ripgrep was accepted")
	}
	registry, _ = composition.NewRegistry(nil)
	if _, err := mountWithExecutables(ctx, registry, rgA, filepath.Join(t.TempDir(), "missing-sh")); err == nil {
		t.Fatal("missing shell bootstrap was accepted")
	}
}

func TestMountedRegistryResumesLegacyEmptyPlanThenSelectsFourTools(t *testing.T) {
	ctx := context.Background()
	registry, err := composition.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	mount, err := Mount(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer mount.Close(ctx)
	legacy, err := runtime.NewRunPlan(runtime.RunPlanSpec{SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := legacy.Descriptor()
	legacy.Release()
	sealed, err := session.VerifyExtensionPlanForSession("session", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := registry.AcquireResumePlan(ctx, runtime.ResumePlanRequest{SessionID: "session", Plan: sealed})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resumed.ResolveTools(ctx, runtime.ToolScopeContext{SessionID: "session"})
	resumedFingerprint := resumed.Descriptor().Fingerprint
	resumed.Release()
	if err != nil || len(resolved) != 0 || resumedFingerprint != descriptor.Fingerprint {
		t.Fatalf("legacy tools=%d fingerprint=%q err=%v", len(resolved), resumedFingerprint, err)
	}
	current, err := registry.AcquireRunPlan(ctx, runtime.RunPlanRequest{SessionID: "session", Config: structConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	tools, err := current.ResolveTools(ctx, runtime.ToolScopeContext{SessionID: "session", WorkspaceRoot: t.TempDir()})
	if err != nil || len(tools) != 4 {
		t.Fatalf("new tools=%d err=%v", len(tools), err)
	}
}

func structConfig() config.Snapshot { return config.Snapshot{Tools: Config()} }
