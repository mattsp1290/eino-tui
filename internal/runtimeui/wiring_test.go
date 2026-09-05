package runtimeui

import (
	"context"
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
