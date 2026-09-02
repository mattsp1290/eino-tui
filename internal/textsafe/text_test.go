package textsafe

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDisplaySanitizesTerminalControls(t *testing.T) {
	in := "\x1b]0;secret\a\x1b[31mred\x1b[0m\r\nhello\u202e\x00 👩‍💻"
	got := Display(in)
	if got != "red\nhello 👩‍💻" {
		t.Fatalf("Display = %q", got)
	}
}

func TestInputNormalizationPolicy(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"cursor and color", "a\x1b[2J\x1b[31mb\x1b[0m", "ab"},
		{"osc hyperlink", "\x1b]8;;https://secret.example\aopen\x1b]8;;\a", "open"},
		{"newlines", "a\r\nb\rc", "a\nb\nc"},
		{"controls", "a\x00\x07\u0085b", "ab"},
		{"unicode", "é 👩‍💻\t界", "é 👩‍💻\t界"},
		{"bidi isolates", "a\u2066secret\u2069b", "asecretb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Input(test.input)
			if err != nil || got != test.want {
				t.Fatalf("Input=%q, %v", got, err)
			}
		})
	}
	invalid := string([]byte{'a', 0xff, 'b'})
	got, err := Input(invalid)
	if err != nil || !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8 result=%q, %v", got, err)
	}
}

func TestPromptLimits(t *testing.T) {
	if _, err := Prompt(" \n\t"); !errors.Is(err, ErrBlankPrompt) {
		t.Fatalf("blank err = %v", err)
	}
	want := strings.Repeat("a", MaxPromptBytes)
	if got, err := Prompt(want); err != nil || got != want {
		t.Fatalf("boundary: %d %v", len(got), err)
	}
	if _, err := Prompt(want + "a"); !errors.Is(err, ErrPromptTooLarge) {
		t.Fatalf("large err = %v", err)
	}
}

func TestDisplayTruncatesAtRuneBoundary(t *testing.T) {
	got := Display(strings.Repeat("界", MaxDisplayBytes))
	if !utf8.ValidString(got) || !strings.HasSuffix(got, TruncatedMarker) || len(got) > MaxDisplayBytes {
		t.Fatalf("invalid truncation: %d", len(got))
	}
}
