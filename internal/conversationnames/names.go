// Package conversationnames owns the pure title policy: numbered fallbacks,
// first-message excerpts, and validation of explicit titles. Eino Agent owns
// durable mutation; this package never touches storage.
package conversationnames

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

const (
	// MaxExcerptRunes bounds the first-message excerpt in Unicode code points.
	MaxExcerptRunes = 60
	// MaxTitleRunes bounds explicit manual and model titles in code points.
	MaxTitleRunes = 256
	// MaxTitleBytes bounds explicit titles in UTF-8 bytes, below the upstream
	// 16384-byte ceiling.
	MaxTitleBytes = 1024
	// MaxRawToolInputBytes bounds raw rename tool arguments before decoding.
	MaxRawToolInputBytes = 8 * 1024

	fallbackPrefix   = "Conversation "
	excerptSeparator = " — "
	ellipsis         = "…"
)

// ErrInvalidTitle is the content-free rejection for every explicit title problem.
var ErrInvalidTitle = errors.New("invalid conversation title")

// Fallback is the display name for a conversation without a durable title.
func Fallback(number uint64) string { return fallbackPrefix + strconv.FormatUint(number, 10) }

// Excerpt derives the bounded display-safe fragment of a first admitted message.
// It measures code points and appends an ellipsis only when truncation occurred.
func Excerpt(message string) string {
	normalized := sanitize(message)
	if utf8.RuneCountInString(normalized) <= MaxExcerptRunes {
		return normalized
	}
	runes := []rune(normalized)
	return strings.TrimRight(string(runes[:MaxExcerptRunes]), " ") + ellipsis
}

// Default is the durable title initialized from the first admitted user
// message. An excerpt that sanitizes to nothing still yields a nonempty title.
func Default(number uint64, firstMessage string) string {
	excerpt := Excerpt(firstMessage)
	if excerpt == "" {
		return Fallback(number)
	}
	return Fallback(number) + excerptSeparator + excerpt
}

// NormalizeTitle validates an explicit manual or model title. Invalid UTF-8,
// blank results, and oversized inputs are rejected without echoing the value.
// Nothing is truncated silently.
func NormalizeTitle(input string) (string, error) {
	if !utf8.ValidString(input) || len(input) > 4*MaxTitleBytes {
		return "", ErrInvalidTitle
	}
	normalized := sanitize(input)
	if normalized == "" || len(normalized) > MaxTitleBytes || utf8.RuneCountInString(normalized) > MaxTitleRunes {
		return "", ErrInvalidTitle
	}
	return normalized, nil
}

// Display renders a stored title for the terminal. Stored titles may have been
// written by other readers of the database, so they are sanitized and bounded
// again here; an empty title shows the numbered fallback.
func Display(title string, number uint64) string {
	normalized := sanitize(title)
	if normalized == "" {
		return Fallback(number)
	}
	if utf8.RuneCountInString(normalized) > MaxTitleRunes {
		runes := []rune(normalized)
		normalized = strings.TrimRight(string(runes[:MaxTitleRunes]), " ") + ellipsis
	}
	return normalized
}

func sanitize(value string) string {
	return strings.Join(strings.Fields(textsafe.Display(value)), " ")
}
