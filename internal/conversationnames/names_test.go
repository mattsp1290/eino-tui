package conversationnames

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFallback(t *testing.T) {
	if got := Fallback(7); got != "Conversation 7" {
		t.Fatalf("Fallback(7) = %q", got)
	}
}

func TestDefaultFallsBackWhenExcerptSanitizesToEmpty(t *testing.T) {
	if got := Default(1, ""); got != "Conversation 1" {
		t.Fatalf("Default(1, \"\") = %q", got)
	}
	// ANSI escape plus a right-to-left override control character both
	// sanitize away entirely, leaving an empty excerpt.
	if got := Default(1, "\x1b[31m‮"); got != "Conversation 1" {
		t.Fatalf("Default(1, ansi+rtl) = %q", got)
	}
}

func TestDefaultBuildsExcerptFromFirstMessage(t *testing.T) {
	got := Default(2, "hello\n  world\tagain")
	want := "Conversation 2 — hello world again"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSanitizeCollapsesMultilineAndControlCharacters(t *testing.T) {
	got := Default(5, "line one\n\n\nline   two")
	want := "Conversation 5 — line one line two"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExcerptTruncatesMultibyteWithoutSplittingRunes(t *testing.T) {
	message := strings.Repeat("界", 61)
	got := Excerpt(message)
	want := strings.Repeat("界", 60) + "…"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated excerpt is not valid UTF-8")
	}
}

func TestExcerptExactBoundaryHasNoEllipsis(t *testing.T) {
	message := strings.Repeat("界", 60)
	got := Excerpt(message)
	if got != message {
		t.Fatalf("got %q, want unchanged message %q", got, message)
	}
	if strings.Contains(got, "…") {
		t.Fatal("unexpected ellipsis at exact 60-code-point boundary")
	}
}

func TestNormalizeTitleRejectsInvalidInputs(t *testing.T) {
	cases := map[string]string{
		"empty":                "",
		"blank spaces":         "   ",
		"blank tab newline":    "\t\n",
		"invalid utf8":         string([]byte{0xff}),
		"too many code points": strings.Repeat("a", 257),
		"too many bytes":       strings.Repeat("界", 342), // 1026 bytes, exceeds MaxTitleBytes
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeTitle(input); !errors.Is(err, ErrInvalidTitle) {
				t.Fatalf("NormalizeTitle(%q) err = %v, want ErrInvalidTitle", input, err)
			}
		})
	}
}

func TestNormalizeTitleAcceptsBoundaryValuesAndCollapsesWhitespace(t *testing.T) {
	at256Runes := strings.Repeat("a", 256)
	if utf8.RuneCountInString(at256Runes) != MaxTitleRunes {
		t.Fatalf("fixture code points = %d, want %d", utf8.RuneCountInString(at256Runes), MaxTitleRunes)
	}
	got, err := NormalizeTitle(at256Runes)
	if err != nil || got != at256Runes {
		t.Fatalf("got %q, err %v", got, err)
	}

	at1024Bytes := strings.Repeat("😀", 256)
	if len(at1024Bytes) != MaxTitleBytes {
		t.Fatalf("fixture bytes = %d, want %d", len(at1024Bytes), MaxTitleBytes)
	}
	got, err = NormalizeTitle(at1024Bytes)
	if err != nil || got != at1024Bytes {
		t.Fatalf("got %q, err %v", got, err)
	}

	got, err = NormalizeTitle("  Hello   world  ")
	if err != nil || got != "Hello world" {
		t.Fatalf("got %q, err %v, want %q", got, err, "Hello world")
	}
}

func TestDisplayFallsBackWhenBlank(t *testing.T) {
	if got := Display("", 3); got != "Conversation 3" {
		t.Fatalf("Display(\"\", 3) = %q", got)
	}
}

func TestDisplayTruncatesStoredTitleToRuneBound(t *testing.T) {
	stored := strings.Repeat("界", 300)
	want := strings.Repeat("界", MaxTitleRunes) + "…"
	got := Display(stored, 9)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatal("displayed title is not valid UTF-8")
	}
}

func TestDisplayStripsAnsiAndControlSequences(t *testing.T) {
	input := "\x1b[31mAlert\x1b[0m Title"
	if got := Display(input, 1); got != "Alert Title" {
		t.Fatalf("got %q", got)
	}
}
