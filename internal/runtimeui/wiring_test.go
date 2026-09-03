package runtimeui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
)

func TestOpenValidatesConfigurationBeforeSQLite(t *testing.T) {
	database := filepath.Join(t.TempDir(), "must-not-exist.db")
	valid := Config{
		Resolver: demomodel.Resolver(nil), Selection: model.Selection{ProviderID: demomodel.ProviderID, ModelID: demomodel.ModelID},
		AgentName: "fixture", SystemPrompt: "test", Display: DisplayMetadata{Provider: "Codex", Model: "fixture"},
	}
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{name: "resolver", edit: func(c *Config) { c.Resolver = nil }},
		{name: "provider", edit: func(c *Config) { c.Selection.ProviderID = "" }},
		{name: "model", edit: func(c *Config) { c.Selection.ModelID = "" }},
		{name: "variant", edit: func(c *Config) { c.Selection.Variant = "mutable" }},
		{name: "agent", edit: func(c *Config) { c.AgentName = "" }},
		{name: "prompt", edit: func(c *Config) { c.SystemPrompt = "" }},
		{name: "display control", edit: func(c *Config) { c.Display.Model = "model\nTOKEN" }},
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
