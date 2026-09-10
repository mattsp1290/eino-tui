package runtimeui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/session/history"
	"github.com/mattsp1290/eino-tui/internal/conversationnames"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

const (
	titleInitializationTimeout = 2 * time.Second
	titleRefreshTimeout        = 500 * time.Millisecond
	firstMessagePageSize       = 20
	maxDirectoryScanPages      = 10000
)

// conversation is the controller's copy of one durable conversation. Identity
// fields are immutable; title is refreshed from the store after every write.
type conversation struct {
	id         session.ID
	number     uint64
	title      string
	createdAt  time.Time
	generation uint64
}

func (c conversation) info() ConversationInfo {
	return ConversationInfo{ID: c.id, Generation: c.generation, Number: c.number, Title: conversationnames.Display(c.title, c.number)}
}

func (c conversation) requestMetadata() map[string]string { return numberMetadata(c.number) }

func numberMetadata(number uint64) map[string]string {
	return map[string]string{ConversationNumberKey: strconv.FormatUint(number, 10)}
}

func (s *service) currentInfoLocked() ConversationInfo {
	if s.selected == nil {
		return ConversationInfo{}
	}
	return s.selected.info()
}

// updateTitle refreshes the controller copy when the same conversation is
// still selected. A later selection is never touched.
func (s *service) updateTitle(id session.ID, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected != nil && s.selected.id == id {
		s.selected.title = title
	}
}

// validateConversation authorizes a durable record for this workspace. A
// stored selector is not authorization: workspace, directory, parent, and the
// immutable number metadata must all match what this application writes.
func (s *service) validateConversation(ctx context.Context, id session.ID) (conversation, error) {
	record, err := s.store.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return conversation{}, err
		}
		if ctx.Err() != nil {
			return conversation{}, ctx.Err()
		}
		return conversation{}, fmt.Errorf("%w", ErrConversationUnavailable)
	}
	if record.ID != id || record.ParentID != "" || record.WorkspaceID != s.workspace.ID || record.Directory != s.workspace.Root || len(record.Metadata) != 1 {
		return conversation{}, fmt.Errorf("%w", ErrConversationUnavailable)
	}
	number, err := strconv.ParseUint(record.Metadata[ConversationNumberKey], 10, 64)
	if err != nil || number == 0 {
		return conversation{}, fmt.Errorf("%w", ErrConversationUnavailable)
	}
	return conversation{id: id, number: number, title: record.Title, createdAt: record.CreatedAt}, nil
}

func (s *service) createSession(ctx context.Context, number uint64) (conversation, error) {
	id := platform.IDs{}.NewConversationID()
	now := time.Now().UTC()
	record := session.Session{
		ID: id, WorkspaceID: s.workspace.ID, Directory: s.workspace.Root, Title: "",
		Metadata: numberMetadata(number), CreatedAt: now, UpdatedAt: now,
	}
	created, err := s.store.CreateSession(ctx, record)
	if err != nil {
		return conversation{}, fmt.Errorf("%w", ErrConversationUnavailable)
	}
	return conversation{id: created.ID, number: number, title: created.Title, createdAt: created.CreatedAt}, nil
}

// listPage reads one authoritative directory page from the root reader.
func (s *service) listPage(ctx context.Context, cursor string) (session.SessionDiscoveryPage, error) {
	page, err := s.store.ListSessions(ctx, session.SessionDiscoveryQuery{WorkspaceID: s.workspace.ID, Limit: DirectoryPageSize, Cursor: cursor})
	if err != nil {
		if ctx.Err() != nil {
			return session.SessionDiscoveryPage{}, ctx.Err()
		}
		if errors.Is(err, session.ErrDiscoveryCursor) {
			return session.SessionDiscoveryPage{}, fmt.Errorf("%w", ErrDirectoryCursor)
		}
		return session.SessionDiscoveryPage{}, fmt.Errorf("%w", ErrDirectoryUnavailable)
	}
	return page, nil
}

// newestConversation returns the first valid record in creation order.
func (s *service) newestConversation(ctx context.Context) (conversation, bool, error) {
	cursor := ""
	for range maxDirectoryScanPages {
		page, err := s.listPage(ctx, cursor)
		if err != nil {
			return conversation{}, false, err
		}
		for _, summary := range page.Sessions {
			conv, err := s.validateConversation(ctx, summary.ID)
			if err == nil {
				return conv, true, nil
			}
			if ctx.Err() != nil {
				return conversation{}, false, ctx.Err()
			}
		}
		if page.NextCursor == "" {
			return conversation{}, false, nil
		}
		cursor = page.NextCursor
	}
	return conversation{}, false, fmt.Errorf("%w", ErrDirectoryUnavailable)
}

// highestNumber scans the authoritative directory for the largest immutable
// number, so a missing preference record never reuses a number.
func (s *service) highestNumber(ctx context.Context) (uint64, error) {
	var highest uint64
	cursor := ""
	for range maxDirectoryScanPages {
		page, err := s.listPage(ctx, cursor)
		if err != nil {
			return 0, err
		}
		for _, summary := range page.Sessions {
			conv, err := s.validateConversation(ctx, summary.ID)
			if err != nil {
				if ctx.Err() != nil {
					return 0, ctx.Err()
				}
				continue
			}
			if conv.number > highest {
				highest = conv.number
			}
		}
		if page.NextCursor == "" {
			return highest, nil
		}
		cursor = page.NextCursor
	}
	return 0, fmt.Errorf("%w", ErrDirectoryUnavailable)
}

// projectConversation builds the destination snapshot before any selection is
// committed. It never runs a model or takes a run lease.
func (s *service) projectConversation(ctx context.Context, conv *conversation) (Snapshot, *session.Run, error) {
	s.retryDefaultTitle(ctx, conv)
	messages, err := loadHistory(ctx, s.store, conv.id)
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		return Snapshot{}, nil, fmt.Errorf("%w", ErrUnavailable)
	}
	active, activeErr := s.store.ActiveRun(ctx, conv.id)
	if activeErr == nil && !active.Terminal() {
		return Snapshot{Messages: messages, Phase: PhaseRecoveryWaiting, Notice: NoticeRecoveryWaiting, RecoveryAt: active.LeaseUntil}, &active, nil
	}
	if activeErr != nil && !errors.Is(activeErr, session.ErrNotFound) {
		return Snapshot{}, nil, fmt.Errorf("%w", ErrUnavailable)
	}
	return Snapshot{Messages: messages, Phase: PhaseIdle}, nil, nil
}

func (s *service) retryDefaultTitle(ctx context.Context, conv *conversation) {
	if conv.title != "" {
		return
	}
	title, err := ensureDefaultTitle(ctx, s.store, s.workspace.ID, conv.id, conv.number)
	if err == nil && title != "" {
		conv.title = title
		s.updateTitle(conv.id, title)
	}
}

// refreshTitle reads the committed title after a run or rename settles.
func (s *service) refreshTitle(ctx context.Context, conv *conversation) {
	record, err := s.store.GetSession(ctx, conv.id)
	if err != nil {
		return
	}
	conv.title = record.Title
	if conv.title == "" {
		s.retryDefaultTitle(ctx, conv)
		return
	}
	s.updateTitle(conv.id, conv.title)
}

// ensureDefaultTitle initializes the durable default title exactly once, in
// the owning store's writer transaction. A nonempty title is never rewritten,
// so a manual or agent rename that wins first stays intact.
func ensureDefaultTitle(ctx context.Context, store session.Store, workspaceID string, id session.ID, number uint64) (string, error) {
	var title string
	err := store.WithinTx(ctx, func(ctx context.Context, tx session.Store) error {
		record, err := tx.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if record.Title != "" {
			title = record.Title
			return nil
		}
		first, found, err := firstUserText(ctx, tx, id)
		if err != nil || !found {
			return err
		}
		title = conversationnames.Default(number, first)
		_, err = tx.SetSessionTitle(ctx, session.SessionTitleRequest{SessionID: id, WorkspaceID: workspaceID, Title: title})
		return err
	})
	if err != nil {
		return "", err
	}
	return title, nil
}

// firstUserText finds the earliest admitted user message's display text using
// the public replay projection, excluding reasoning and provider state.
func firstUserText(ctx context.Context, store session.Store, id session.ID) (string, bool, error) {
	cursor := session.ReplayCursor{Limit: firstMessagePageSize}
	for {
		batch, err := store.ListMessages(ctx, id, cursor)
		if err != nil {
			return "", false, err
		}
		owners, err := session.ResolveReplayPartOwners(batch.Parts, batch.PartOwnerMessageIDs)
		if err != nil {
			return "", false, err
		}
		for _, durable := range batch.Messages {
			if durable.Role != session.RoleUser {
				continue
			}
			var parts []session.Part
			for i, part := range batch.Parts {
				if owners[i] == durable.ID && part.Kind != session.PartToolCall && part.Kind != session.PartToolResult {
					parts = append(parts, part)
				}
			}
			projected, err := history.Project(session.ReplayBatch{Messages: []session.Message{durable}, Parts: parts}, history.Options{IncludeReasoning: false, IncludeState: false})
			if err != nil {
				return "", false, err
			}
			if len(projected) > 0 && projected[0] != nil {
				return projected[0].Content, true, nil
			}
			return "", true, nil
		}
		if batch.Next == (session.ReplayCursor{}) {
			return "", false, nil
		}
		cursor = batch.Next
	}
}

func (s *service) preferenceError(err error) error {
	switch {
	case errors.Is(err, platform.ErrPreferenceReconcile):
		s.mu.Lock()
		s.reconcile = true
		s.mu.Unlock()
		return fmt.Errorf("%w", ErrReconciliationRequired)
	case errors.Is(err, platform.ErrPreferenceCorrupt), errors.Is(err, platform.ErrPreferenceWorkspace), errors.Is(err, platform.ErrPreferenceOverflow):
		return fmt.Errorf("%w", ErrPreferenceInvalid)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, platform.ErrPreferenceWrite), errors.Is(err, platform.ErrLockUnavailable):
		return fmt.Errorf("%w", ErrPreferenceFailed)
	}
	return err
}

// ErrPreferenceInvalid reports a corrupt, foreign, or exhausted preference
// record. It is never repaired automatically.
var ErrPreferenceInvalid = errors.New("workspace preferences are invalid")

// resolveSelection performs startup resolution or reconciliation under the
// workspace preference lock, then installs the resolved conversation.
func (s *service) resolveSelection(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return Snapshot{}, ErrClosing
	}
	if s.attempt != nil || (s.state != stateUnresolved && s.state != stateIdle) {
		s.mu.Unlock()
		return Snapshot{}, ErrBusy
	}
	from := s.state
	s.mu.Unlock()
	a, err := s.beginAttempt(ctx, from, stateMutating, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer s.finishPending(a)

	var chosen conversation
	notice := ""
	warning := false
	err = s.prefs.WithWorkspace(a.ctx, s.workspace.ID, func(locked *platform.LockedPreferences) error {
		current := locked.Current()
		if current.SelectedConversationID != "" {
			conv, err := s.validateConversation(a.ctx, session.ID(current.SelectedConversationID))
			switch {
			case err == nil:
				chosen = conv
			case errors.Is(err, session.ErrNotFound):
				notice = NoticeConversationReplaced
			default:
				return err
			}
		}
		if chosen.id == "" {
			if locked.Fresh() {
				highest, err := s.highestNumber(a.ctx)
				if err != nil {
					return err
				}
				if highest > 0 {
					if _, err := locked.EnsureNextNumber(highest + 1); err != nil {
						return err
					}
				}
			}
			newest, found, err := s.newestConversation(a.ctx)
			if err != nil {
				return err
			}
			if found {
				chosen = newest
			} else {
				number, write, err := locked.ReserveNumber()
				if err != nil {
					return err
				}
				warning = write.DurabilityWarning
				chosen, err = s.createSession(a.ctx, number)
				if err != nil {
					return err
				}
			}
		}
		if locked.Fresh() || string(chosen.id) != current.SelectedConversationID {
			write, err := locked.Remember(chosen.id)
			if err != nil {
				return err
			}
			warning = warning || write.DurabilityWarning
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, s.mutationFailure(a, err)
	}
	snapshot, recovery, err := s.projectConversation(a.ctx, &chosen)
	if err != nil {
		return Snapshot{}, s.failAttempt(a, err)
	}
	return s.commitSelection(a, chosen, snapshot, recovery, notice, warning)
}

// commitSelection installs a conversation whose selection is already
// persisted. Caller cancellation no longer matters; only closing does.
func (s *service) commitSelection(a *attempt, chosen conversation, snapshot Snapshot, recovery *session.Run, notice string, warning bool) (Snapshot, error) {
	s.mu.Lock()
	if s.state >= stateClosing || s.attempt != a {
		s.mu.Unlock()
		s.abortAttempt(a)
		return Snapshot{}, ErrClosing
	}
	s.attempt = nil
	s.generation++
	chosen.generation = s.generation
	s.selected = &chosen
	s.reconcile = false
	if recovery != nil {
		s.state = stateWaiting
		s.recoveryRun = *recovery
	} else {
		s.state = stateIdle
		s.recoveryRun = session.Run{}
	}
	s.mu.Unlock()
	a.cancel()
	snapshot.Conversation = chosen.info()
	if snapshot.Notice == "" {
		switch {
		case notice != "":
			snapshot.Notice = notice
		case warning:
			snapshot.Notice = NoticeSelectionUnconfirmed
		}
	}
	return snapshot, nil
}

// beginMutation starts an idle-only, generation-checked exclusive operation.
func (s *service) beginMutation(ctx context.Context, expected uint64) (*attempt, error) {
	a, err := s.beginAttempt(ctx, stateIdle, stateMutating, true)
	if err != nil {
		return nil, err
	}
	if a.conv.generation != expected {
		s.abortAttempt(a)
		s.finishPending(a)
		return nil, ErrStaleGeneration
	}
	return a, nil
}

// failAttempt classifies the attempt state before aborting it, so caller
// cancellation and closing are reported ahead of the mapped failure.
func (s *service) failAttempt(a *attempt, mapped error) error {
	attemptErr := s.attemptError(a)
	s.abortAttempt(a)
	if attemptErr != nil && !errors.Is(mapped, ErrReconciliationRequired) && !errors.Is(mapped, ErrPreferenceFailed) {
		return attemptErr
	}
	return mapped
}

func (s *service) mutationFailure(a *attempt, err error) error {
	return s.failAttempt(a, s.preferenceError(err))
}

// ListConversations returns one bounded page in upstream creation order. It
// performs no execution, recovery, or provider call and is cancellable.
func (s *service) ListConversations(ctx context.Context, cursor string) (ConversationPage, error) {
	readCtx, release, err := s.beginRead(ctx)
	if err != nil {
		return ConversationPage{}, err
	}
	defer release()
	page, err := s.listPage(readCtx, cursor)
	if err != nil {
		return ConversationPage{}, err
	}
	s.mu.Lock()
	var selectedID session.ID
	if s.selected != nil {
		selectedID = s.selected.id
	}
	s.mu.Unlock()
	items := make([]ConversationSummary, 0, len(page.Sessions))
	for _, summary := range page.Sessions {
		item := ConversationSummary{ID: summary.ID, CreatedAt: summary.CreatedAt, Selected: summary.ID == selectedID}
		conv, err := s.validateConversation(readCtx, summary.ID)
		if err != nil {
			if readCtx.Err() != nil {
				return ConversationPage{}, readCtx.Err()
			}
			item.Unavailable = true
			item.Title = unavailableConversationTitle
		} else {
			item.Number = conv.number
			item.Title = conversationnames.Display(summary.Title, conv.number)
		}
		items = append(items, item)
	}
	return ConversationPage{Items: items, NextCursor: page.NextCursor}, nil
}

const unavailableConversationTitle = "Unavailable conversation"

func (s *service) beginRead(caller context.Context) (context.Context, func(), error) {
	if err := caller.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return nil, nil, ErrClosing
	}
	s.pending.Add(1)
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(s.readCtx)
	stop := context.AfterFunc(caller, cancel)
	return ctx, func() { stop(); cancel(); s.pending.Done() }, nil
}

// CreateConversation reserves a number, creates an empty durable session, and
// selects it. Failures after creation keep the record discoverable and report
// its ID so callers retry selection instead of creating again.
func (s *service) CreateConversation(ctx context.Context, expected uint64) (SelectionResult, error) {
	a, err := s.beginMutation(ctx, expected)
	if err != nil {
		return SelectionResult{}, err
	}
	defer s.finishPending(a)
	var created conversation
	var committed session.ID
	warning := false
	err = s.prefs.WithWorkspace(a.ctx, s.workspace.ID, func(locked *platform.LockedPreferences) error {
		number, write, err := locked.ReserveNumber()
		if err != nil {
			return err
		}
		warning = write.DurabilityWarning
		created, err = s.createSession(a.ctx, number)
		if err != nil {
			return err
		}
		committed = created.id
		remembered, err := locked.Remember(created.id)
		if err != nil {
			return err
		}
		warning = warning || remembered.DurabilityWarning
		return nil
	})
	if err != nil {
		return SelectionResult{CommittedID: committed}, s.mutationFailure(a, err)
	}
	snapshot, recovery, err := s.projectConversation(a.ctx, &created)
	if err != nil {
		return SelectionResult{CommittedID: committed}, s.failAttempt(a, fmt.Errorf("%w", ErrConversationUnavailable))
	}
	installed, err := s.commitSelection(a, created, snapshot, recovery, "", warning)
	if err != nil {
		return SelectionResult{CommittedID: committed}, err
	}
	return SelectionResult{Snapshot: installed, DurabilityWarning: warning, CommittedID: committed}, nil
}

// SelectConversation validates and loads the destination before replacing the
// current selection, then persists the choice as the commit point.
func (s *service) SelectConversation(ctx context.Context, id session.ID, expected uint64) (SelectionResult, error) {
	a, err := s.beginMutation(ctx, expected)
	if err != nil {
		return SelectionResult{}, err
	}
	defer s.finishPending(a)
	target, err := s.validateConversation(a.ctx, id)
	if err != nil {
		return SelectionResult{}, s.failAttempt(a, fmt.Errorf("%w", ErrConversationUnavailable))
	}
	snapshot, recovery, err := s.projectConversation(a.ctx, &target)
	if err != nil {
		return SelectionResult{}, s.failAttempt(a, fmt.Errorf("%w", ErrConversationUnavailable))
	}
	var write platform.PreferenceWrite
	err = s.prefs.WithWorkspace(a.ctx, s.workspace.ID, func(locked *platform.LockedPreferences) error {
		var err error
		write, err = locked.Remember(target.id)
		return err
	})
	if err != nil {
		return SelectionResult{}, s.mutationFailure(a, err)
	}
	installed, err := s.commitSelection(a, target, snapshot, recovery, "", write.DurabilityWarning)
	if err != nil {
		return SelectionResult{}, err
	}
	return SelectionResult{Snapshot: installed, DurabilityWarning: write.DurabilityWarning}, nil
}

// RenameConversation writes a validated manual title for the selected
// conversation and refreshes its display metadata. History, numbering, model
// choice, and remembered selection are untouched.
func (s *service) RenameConversation(ctx context.Context, id session.ID, expected uint64, title string) (ConversationInfo, error) {
	normalized, err := conversationnames.NormalizeTitle(title)
	if err != nil {
		return ConversationInfo{}, fmt.Errorf("%w", ErrInvalidTitle)
	}
	a, err := s.beginMutation(ctx, expected)
	if err != nil {
		return ConversationInfo{}, err
	}
	defer s.finishPending(a)
	if a.conv.id != id {
		s.abortAttempt(a)
		return ConversationInfo{}, fmt.Errorf("%w", ErrConversationUnavailable)
	}
	_, writeErr := s.store.SetSessionTitle(a.ctx, session.SessionTitleRequest{SessionID: id, WorkspaceID: s.workspace.ID, Title: normalized})
	conv := a.conv
	// Reconcile to the committed title even when the write raced cancellation.
	refreshCtx, cancel := context.WithTimeout(s.ctx, titleRefreshTimeout)
	record, readErr := s.store.GetSession(refreshCtx, id)
	cancel()
	if readErr == nil {
		conv.title = record.Title
		s.updateTitle(id, record.Title)
	}
	if writeErr != nil && (readErr != nil || record.Title != normalized) {
		switch {
		case errors.Is(writeErr, context.Canceled), errors.Is(writeErr, context.DeadlineExceeded):
			return ConversationInfo{}, s.failAttempt(a, context.Canceled)
		case errors.Is(writeErr, session.ErrNotFound), errors.Is(writeErr, session.ErrConflict):
			return ConversationInfo{}, s.failAttempt(a, fmt.Errorf("%w", ErrConversationUnavailable))
		default:
			return ConversationInfo{}, s.failAttempt(a, fmt.Errorf("%w", ErrUnavailable))
		}
	}
	if readErr != nil {
		conv.title = normalized
		s.updateTitle(id, normalized)
	}
	s.mu.Lock()
	if s.state >= stateClosing || s.attempt != a {
		s.mu.Unlock()
		s.abortAttempt(a)
		return ConversationInfo{}, ErrClosing
	}
	s.attempt = nil
	s.state = stateIdle
	info := s.currentInfoLocked()
	s.mu.Unlock()
	a.cancel()
	return info, nil
}
