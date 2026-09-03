package codexmodel

import (
	"errors"
	"regexp"

	codexauth "github.com/mattsp1290/codex-auth-go"
	agentmodel "github.com/mattsp1290/eino-agent/model"
)

const (
	ProviderID   = agentmodel.ProviderID("openai-codex")
	AppName      = "eino-tui"
	DefaultModel = "gpt-5.5"
)

var (
	ErrInvalidModel = errors.New("invalid Codex model")
	modelPattern    = regexp.MustCompile(`^gpt-[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[a-z0-9]+(?:-[a-z0-9]+)*)?$`)
)

// ValidateModel accepts only one canonical, endpoint-admitted model ID.
func ValidateModel(value string) error {
	if value == "" || len(value) > agentmodel.MaxProviderStateModelIDBytes || !modelPattern.MatchString(value) || !codexauth.IsCodexAllowed(value) {
		return ErrInvalidModel
	}
	return nil
}
