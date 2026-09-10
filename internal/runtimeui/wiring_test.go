package runtimeui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

func TestOpenValidatesConfigurationBeforeSQLite(t *testing.T) {
	database := filepath.Join(t.TempDir(), "must-not-exist.db")
	paths := platform.Paths{Database: database, Workspaces: t.TempDir()}
	workspace := platform.Workspace{Root: t.TempDir(), ID: platform.WorkspaceID("/x")}
	valid := Config{
		Resolver:  demomodel.Resolver(nil),
		AgentName: "fixture", SystemPrompt: "test",
	}
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{name: "resolver", edit: func(c *Config) { c.Resolver = nil }},
		{name: "agent", edit: func(c *Config) { c.AgentName = "" }},
		{name: "prompt", edit: func(c *Config) { c.SystemPrompt = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.edit(&cfg)
			if _, err := Open(context.Background(), paths, workspace, cfg); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if _, err := os.Stat(database); !os.IsNotExist(err) {
				t.Fatalf("SQLite touched before validation: %v", err)
			}
		})
	}
}

func TestOpenFailsClosedWhenReadOnlyCatalogIsUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := platform.IdentifyWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Resolver: demomodel.DynamicResolver(nil), AgentName: "fixture", SystemPrompt: "test"}
	service, err := Open(ctx, paths, workspace, cfg)
	if service != nil || !errors.Is(err, ErrToolsUnavailable) {
		t.Fatalf("service=%v error=%v", service, err)
	}
	// A second SQLite open proves the failed constructor released its handle.
	store, openErr := os.OpenFile(paths.Database, os.O_RDWR, 0)
	if openErr != nil {
		t.Fatal(openErr)
	}
	if closeErr := store.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}
