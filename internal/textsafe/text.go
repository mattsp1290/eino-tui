package textsafe

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const (
	MaxDisplayBytes = 256 << 10
	MaxPromptBytes  = 64 << 10
	TruncatedMarker = "\n[content truncated]"
)

var (
	ErrBlankPrompt    = errors.New("prompt is blank")
	ErrPromptTooLarge = errors.New("prompt is too large")
)

// Display normalizes untrusted content before it can enter presentation state.
func Display(value string) string { return normalize(value, MaxDisplayBytes, true) }

// Input normalizes editable terminal text without applying blank validation.
func Input(value string) (string, error) {
	normalized := normalize(value, MaxPromptBytes+1, false)
	if len(normalized) > MaxPromptBytes {
		return "", ErrPromptTooLarge
	}
	return normalized, nil
}

// Prompt normalizes terminal input and enforces the durable admission limit.
func Prompt(value string) (string, error) {
	normalized, err := Input(value)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(normalized) == "" {
		return "", ErrBlankPrompt
	}
	return normalized, nil
}

func normalize(value string, limit int, truncate bool) string {
	value = strings.ToValidUTF8(value, "�")
	value = ansi.Strip(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var out strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' || r == '\t':
			out.WriteRune(r)
		case r == 0x200d:
			out.WriteRune(r)
		case r >= 0x202a && r <= 0x202e:
			continue
		case r >= 0x2066 && r <= 0x2069:
			continue
		case unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r):
			continue
		default:
			out.WriteRune(r)
		}
	}
	result := out.String()
	if len(result) <= limit {
		return result
	}
	if !truncate {
		return result
	}
	cut := limit - len(TruncatedMarker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.ValidString(result[:cut]) {
		cut--
	}
	return result[:cut] + TruncatedMarker
}
