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

const (
	RoleUser             Role   = "user"
	RoleAssistant        Role   = "assistant"
	RoleNotice           Role   = "notice"
	StatusComplete       Status = "complete"
	StatusInterrupted    Status = "interrupted"
	StatusFailed         Status = "failed"
	PhaseIdle            Phase  = "idle"
	PhaseStarting        Phase  = "starting"
	PhaseRunning         Phase  = "running"
	PhaseRecoveryWaiting Phase  = "recovery-waiting"
	PhaseRecovering      Phase  = "recovering"
	PhaseClosing         Phase  = "closing"
)

type Message struct {
	ID      string
	Role    Role
	Content string
	Status  Status
}

type Snapshot struct {
	RunID         session.RunID
	Version       uint64
	Terminal      bool
	Resync        bool
	Messages      []Message
	LiveAssistant string
	Phase         Phase
	Notice        string
	RecoveryAt    time.Time
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

type Service interface {
	Load(context.Context) (Snapshot, error)
	Start(context.Context, string, StartConfig) (ActionResult, error)
	InterruptActive(context.Context) error
	Recover(context.Context) (ActionResult, error)
	Close(context.Context) error
}

var (
	ErrBusy          = errors.New("chat is busy")
	ErrInvalidPrompt = errors.New("invalid prompt")
	ErrInvalidConfig = errors.New("invalid run configuration")
	ErrClosing       = errors.New("chat is closing")
	ErrUnavailable   = errors.New("chat is unavailable")
	ErrCloseTimeout  = errors.New("chat shutdown timed out")
)

const (
	NoticeRecoveryWaiting = "Waiting to recover an unfinished local turn…"
	NoticeInterrupted     = "Response interrupted."
	NoticePlanUnavailable = "Your ChatGPT plan does not include Codex access."
	NoticeQuotaExceeded   = "Your Codex quota is exhausted. Try again later."
	NoticeProviderFailed  = "The Codex provider could not complete the response."
	NoticeUnavailable     = "Conversation history is temporarily unavailable."
	NoticeHistoryOmitted  = "Older durable messages are omitted from this view."
)
