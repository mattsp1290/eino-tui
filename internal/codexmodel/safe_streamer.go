package codexmodel

import (
	"context"
	"errors"

	einoschema "github.com/cloudwego/eino/schema"
	codexauth "github.com/mattsp1290/codex-auth-go"
	agentmodel "github.com/mattsp1290/eino-agent/model"
)

type safeProviderStateStreamer struct {
	upstream agentmodel.ProviderStateStreamer
}

func newSafeProviderStateStreamer(upstream agentmodel.ProviderStateStreamer) agentmodel.ProviderStateStreamer {
	return &safeProviderStateStreamer{upstream: upstream}
}

func (s *safeProviderStateStreamer) StreamProvider(ctx context.Context, request agentmodel.Request) (*einoschema.StreamReader[agentmodel.StreamDelta], error) {
	reader, err := s.upstream.StreamProvider(ctx, request)
	if err != nil {
		return nil, safeProviderError(err)
	}
	if reader == nil {
		return nil, safeProviderError(errors.New("provider returned a nil stream"))
	}
	return einoschema.StreamReaderWithConvert(
		reader,
		func(delta agentmodel.StreamDelta) (agentmodel.StreamDelta, error) { return delta, nil },
		einoschema.WithErrWrapper(safeProviderError),
	), nil
}

func (s *safeProviderStateStreamer) ProviderStateContract() agentmodel.ProviderStateContract {
	return s.upstream.ProviderStateContract()
}

func (s *safeProviderStateStreamer) CaptureProviderState(message *einoschema.Message) (agentmodel.ProviderStateCapture, error) {
	return s.upstream.CaptureProviderState(message)
}

func safeProviderError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, codexauth.ErrPlanNotIncluded):
		return agentmodel.Error{Code: "codex_plan_unavailable", Message: "Codex plan unavailable", Cause: codexauth.ErrPlanNotIncluded}
	case errors.Is(err, codexauth.ErrQuotaExceeded):
		return agentmodel.Error{Code: "codex_quota_exceeded", Message: "Codex quota exceeded", Cause: codexauth.ErrQuotaExceeded}
	case errors.Is(err, agentmodel.ErrProviderStateInvalid):
		return agentmodel.Error{Code: "codex_provider_state_invalid", Message: "Codex provider state invalid", Cause: agentmodel.ErrProviderStateInvalid}
	case errors.Is(err, agentmodel.ErrProviderStateTooLarge):
		return agentmodel.Error{Code: "codex_provider_state_too_large", Message: "Codex provider state too large", Cause: agentmodel.ErrProviderStateTooLarge}
	case errors.Is(err, agentmodel.ErrProviderStateMismatch):
		return agentmodel.Error{Code: "codex_provider_state_mismatch", Message: "Codex provider state mismatch", Cause: agentmodel.ErrProviderStateMismatch}
	case errors.Is(err, agentmodel.ErrProviderStateVersion):
		return agentmodel.Error{Code: "codex_provider_state_version", Message: "Codex provider state version unsupported", Cause: agentmodel.ErrProviderStateVersion}
	default:
		return agentmodel.Error{Code: "codex_provider_failed", Message: "Codex provider request failed"}
	}
}
