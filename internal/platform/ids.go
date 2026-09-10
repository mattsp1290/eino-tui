package platform

import (
	"github.com/google/uuid"
	"github.com/mattsp1290/eino-agent/session"
)

// IDs implements runtime.IDGenerator with collision-resistant typed values.
type IDs struct{}

func typedID(prefix string) string { return prefix + uuid.NewString() }

func (IDs) NewRunID() session.RunID           { return session.RunID(typedID("run-")) }
func (IDs) NewMessageID() session.MessageID   { return session.MessageID(typedID("message-")) }
func (IDs) NewPartID() session.PartID         { return session.PartID(typedID("part-")) }
func (IDs) NewToolCallID() session.ToolCallID { return session.ToolCallID(typedID("tool-call-")) }
func (IDs) NewEventID() session.EventID       { return session.EventID(typedID("event-")) }
func (IDs) NewEpochID() session.EpochID       { return session.EpochID(typedID("epoch-")) }

// NewConversationID allocates a random durable conversation identity. Two
// conversations in one workspace never share an ID, and the ID carries no
// workspace, path, or ordering information.
func (IDs) NewConversationID() session.ID { return session.ID(typedID("conversation-")) }
