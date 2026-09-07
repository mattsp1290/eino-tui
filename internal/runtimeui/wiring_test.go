package runtimeui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattsp1290/eino-tui/internal/demomodel"
)

func TestOpenValidatesConfigurationBeforeSQLite(t *testing.T) {
	database := filepath.Join(t.TempDir(), "must-not-exist.db")
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
			if _, err := Open(context.Background(), database, "session", t.TempDir(), cfg); err == nil {
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
	database := filepath.Join(t.TempDir(), "catalog.db")
	cfg := Config{Resolver: demomodel.DynamicResolver(nil), AgentName: "fixture", SystemPrompt: "test"}
	service, err := Open(context.Background(), database, "session", t.TempDir(), cfg)
	if service != nil || !errors.Is(err, ErrToolsUnavailable) {
		t.Fatalf("service=%v error=%v", service, err)
	}
	// A second SQLite open proves the failed constructor released its handle.
	store, openErr := os.OpenFile(database, os.O_RDWR, 0)
	if openErr != nil {
		t.Fatal(openErr)
	}
	if closeErr := store.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}
