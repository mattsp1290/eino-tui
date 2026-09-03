package codexmodel

import (
	"errors"
	"strings"
	"testing"

	agentmodel "github.com/mattsp1290/eino-agent/model"
)

func TestValidateModel(t *testing.T) {
	valid256 := "gpt-5.5-" + strings.Repeat("a", agentmodel.MaxProviderStateModelIDBytes-len("gpt-5.5-"))
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "default", value: DefaultModel, valid: true},
		{name: "allowed suffix", value: "gpt-5.5-codex", valid: true},
		{name: "maximum bytes", value: valid256, valid: true},
		{name: "empty"},
		{name: "old", value: "gpt-5.2"},
		{name: "prefix", value: "openai/gpt-5.5"},
		{name: "uppercase", value: "GPT-5.5"},
		{name: "space", value: "gpt-5.5 "},
		{name: "newline", value: "gpt-5.5\n"},
		{name: "bad suffix", value: "gpt-5.5--codex"},
		{name: "over bytes", value: valid256 + "a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateModel(test.value)
			if test.valid && err != nil {
				t.Fatalf("valid model rejected: %v", err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidModel) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestConfigurationConstants(t *testing.T) {
	if ProviderID != "openai-codex" || AppName != "eino-tui" || DefaultModel != "gpt-5.5" {
		t.Fatalf("unexpected constants: %q %q %q", ProviderID, AppName, DefaultModel)
	}
}
