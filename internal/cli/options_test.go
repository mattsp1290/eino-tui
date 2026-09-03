package cli

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		kind  Command
		model string
		valid bool
	}{
		{name: "chat", valid: true, kind: CommandChat, model: "gpt-5.5"},
		{name: "model", args: []string{"--model", "gpt-5.6"}, valid: true, kind: CommandChat, model: "gpt-5.6"},
		{name: "login", args: []string{"login"}, valid: true, kind: CommandLogin},
		{name: "status", args: []string{"status"}, valid: true, kind: CommandStatus},
		{name: "help", args: []string{"--help"}, valid: true, kind: CommandHelp},
		{name: "version", args: []string{"--version"}, valid: true, kind: CommandVersion},
		{name: "missing model", args: []string{"--model"}},
		{name: "empty model", args: []string{"--model", ""}},
		{name: "duplicate", args: []string{"--model", "gpt-5.5", "--model", "gpt-5.6"}},
		{name: "login flag", args: []string{"login", "--device"}},
		{name: "status trailing", args: []string{"status", "extra"}},
		{name: "logout", args: []string{"logout"}},
		{name: "prompt", args: []string{"hello"}},
		{name: "model after command", args: []string{"login", "--model", "gpt-5.5"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.args)
			if test.valid {
				if err != nil || got.Command != test.kind || got.Model != test.model {
					t.Fatalf("Parse() = %#v, %v", got, err)
				}
			} else if err == nil {
				t.Fatalf("invalid arguments accepted: %#v", got)
			}
		})
	}
}
