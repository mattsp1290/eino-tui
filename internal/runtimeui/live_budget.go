package runtimeui

import "github.com/mattsp1290/eino-tui/internal/textsafe"

// liveMessageBudget admits whole durable message groups before they enter a
// snapshot or accumulator. After an overflow, callers keep the admitted prefix
// and wait for terminal reconciliation, just as the streaming path does.
type liveMessageBudget struct {
	textBytes int
	toolCount int
	toolBytes int
}

func (b *liveMessageBudget) admit(message Message) bool {
	textBytes := len(message.Content)
	toolBytes := messageDisplayBytes(message) - textBytes
	return b.reserve(textBytes, len(message.Tools), toolBytes)
}

// reserve charges a delta only when every limit permits it. Replacement deltas
// can be negative when a sanitized text or status representation shrinks.
func (b *liveMessageBudget) reserve(textBytes, toolCount, toolBytes int) bool {
	if textBytes > textsafe.MaxDisplayBytes-b.textBytes ||
		toolCount > MaxLiveToolActivities-b.toolCount ||
		toolBytes > MaxLiveToolDisplayBytes-b.toolBytes {
		return false
	}
	b.textBytes += textBytes
	b.toolCount += toolCount
	b.toolBytes += toolBytes
	return true
}
