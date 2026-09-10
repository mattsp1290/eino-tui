package runtimeui

import (
	"context"
	"errors"
	"time"

	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
)

type Role string
type Status string
type Phase string
type ToolStatus string

const (
	RoleUser             Role       = "user"
	RoleAssistant        Role       = "assistant"
	RoleNotice           Role       = "notice"
	StatusComplete       Status     = "complete"
	StatusInterrupted    Status     = "interrupted"
	StatusFailed         Status     = "failed"
	PhaseIdle            Phase      = "idle"
	PhaseStarting        Phase      = "starting"
	PhaseRunning         Phase      = "running"
	PhaseRecoveryWaiting Phase      = "recovery-waiting"
	PhaseRecovering      Phase      = "recovering"
	PhaseClosing         Phase      = "closing"
	ToolPending          ToolStatus = "pending"
	ToolRunning          ToolStatus = "running"
	ToolCompleted        ToolStatus = "completed"
	ToolFailed           ToolStatus = "failed"
	ToolInterrupted      ToolStatus = "interrupted"
)

// ToolActivity is the bounded, display-safe projection of a durable tool call.
// It intentionally contains no output, error, metadata, or raw arguments.
type ToolActivity struct {
	ID      string
	Name    string
	Subject string
	Status  ToolStatus
}

type Message struct {
	ID      string
	Role    Role
	Content string
	Status  Status
	Tools   []ToolActivity
}

// ConversationInfo is the display-safe identity of the selected conversation.
// Generation increases on every selection change so late results from a
// previous selection can never update a newer screen. ID is never rendered.
type ConversationInfo struct {
	ID         session.ID
	Generation uint64
	Number     uint64
	Title      string
}

type Snapshot struct {
	RunID        session.RunID
	Version      uint64
	Terminal     bool
	Resync       bool
	Messages     []Message
	LiveMessages []Message
	Phase        Phase
	Notice       string
	RecoveryAt   time.Time
	Conversation ConversationInfo
}

type Run interface {
	ID() session.RunID
	Next(context.Context) (Snapshot, bool)
	Finished() <-chan struct{}
}

type ActionKind string

const (
	ActionStarted         ActionKind = "started"
	ActionRecoveryWaiting ActionKind = "recovery-waiting"
)

type ActionResult struct {
	Kind     ActionKind
	Run      Run
	Snapshot Snapshot
}

// StartConfig is the model choice frozen for the next admitted turn.
type StartConfig struct {
	Selection       model.Selection
	ReasoningEffort string
}

// ConversationSummary is one directory row. Unavailable marks a record in this
// workspace that was not created by this application; it cannot be selected.
type ConversationSummary struct {
	ID          session.ID
	Number      uint64
	Title       string
	CreatedAt   time.Time
	Selected    bool
	Unavailable bool
}

// ConversationPage is one bounded directory page in upstream creation order.
type ConversationPage struct {
	Items      []ConversationSummary
	NextCursor string
}

// SelectionResult reports a creation or selection. DurabilityWarning means the
// selection was committed and observed but its crash durability is
// unconfirmed. CommittedID accompanies ErrPreferenceFailed after a creation
// that persisted the session but not the selection, so callers retry selection
// of that ID instead of creating again.
type SelectionResult struct {
	Snapshot          Snapshot
	DurabilityWarning bool
	CommittedID       session.ID
}

// DirectoryPageSize is the bounded page requested from upstream discovery.
const DirectoryPageSize = 50

type Service interface {
	Load(context.Context) (Snapshot, error)
	Start(context.Context, string, StartConfig) (ActionResult, error)
	InterruptActive(context.Context) error
	Recover(context.Context) (ActionResult, error)
	ListConversations(context.Context, string) (ConversationPage, error)
	CreateConversation(context.Context, uint64) (SelectionResult, error)
	SelectConversation(context.Context, session.ID, uint64) (SelectionResult, error)
	RenameConversation(context.Context, session.ID, uint64, string) (ConversationInfo, error)
	Close(context.Context) error
}

var (
	ErrBusy                    = errors.New("chat is busy")
	ErrInvalidPrompt           = errors.New("invalid prompt")
	ErrInvalidConfig           = errors.New("invalid run configuration")
	ErrClosing                 = errors.New("chat is closing")
	ErrUnavailable             = errors.New("chat is unavailable")
	ErrCloseTimeout            = errors.New("chat shutdown timed out")
	ErrToolsUnavailable        = errors.New("read-only workspace tools unavailable")
	ErrNoConversation          = errors.New("no conversation is selected")
	ErrStaleGeneration         = errors.New("conversation selection changed")
	ErrConversationUnavailable = errors.New("conversation unavailable")
	ErrDirectoryUnavailable    = errors.New("conversation directory unavailable")
	ErrDirectoryCursor         = errors.New("conversation directory page expired")
	ErrPreferenceFailed        = errors.New("conversation selection could not be saved")
	ErrReconciliationRequired  = errors.New("conversation selection requires reconciliation")
	ErrInvalidTitle            = errors.New("invalid conversation title")
)

const (
	NoticeRecoveryWaiting      = "Waiting to recover an unfinished local turn…"
	NoticeInterrupted          = "Response interrupted."
	NoticePlanUnavailable      = "Your ChatGPT plan does not include Codex access."
	NoticeQuotaExceeded        = "Your Codex quota is exhausted. Try again later."
	NoticeProviderFailed       = "The Codex provider could not complete the response."
	NoticeUnavailable          = "Conversation history is temporarily unavailable."
	NoticeHistoryOmitted       = "Older durable messages are omitted from this view."
	NoticeTitleUnavailable     = "The conversation title could not be saved yet."
	NoticeConversationReplaced = "The previous conversation is unavailable; opened the newest one."
	NoticeSelectionUnconfirmed = "Selection saved, but its crash durability is unconfirmed."
)

// ConversationNumberKey is the immutable session metadata key carrying the
// per-workspace number. It is written once at creation and passed unchanged on
// every runtime request so upstream identity checks keep working.
const ConversationNumberKey = "eino_tui_conversation_number"
