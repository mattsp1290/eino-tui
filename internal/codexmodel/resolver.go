package codexmodel

import (
	"context"
	"fmt"
	"net/http"

	einomodel "github.com/cloudwego/eino/components/model"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-providers/openaicodex"
)

const (
	ReasoningExtraKey         = "openaicodex:reasoning_items"
	ReasoningCodecID          = "github.com/mattsp1290/eino-providers/openaicodex/reasoning-items"
	ReasoningCodecVersion     = 1
	ReasoningCompatibilityKey = "openaicodex-responses-reasoning-v1"
)

func reasoningLimits() agentmodel.ProviderStateLimits {
	return agentmodel.ProviderStateLimits{
		MaxItems:              32,
		MaxItemBytes:          10 << 20,
		MaxMessageBytes:       16 << 20,
		MaxEnvelopeBytes:      13_985_112,
		MaxStoredMessageBytes: 22_632_024,
	}
}

type chatModelFactory func(context.Context, *http.Client, openaicodex.ChatModelConfig) (einomodel.ToolCallingChatModel, error)

type resolver struct {
	modelID  agentmodel.ID
	streamer agentmodel.Streamer
}

// NewResolver builds the single immutable Codex provider resolver.
func NewResolver(ctx context.Context, client *http.Client, modelID string) (agentmodel.Resolver, error) {
	return newResolver(ctx, client, modelID, openaicodex.NewChatModelWithHTTPClient)
}

func newResolver(ctx context.Context, client *http.Client, modelID string, factory chatModelFactory) (agentmodel.Resolver, error) {
	if client == nil || factory == nil || ValidateModel(modelID) != nil {
		return nil, fmt.Errorf("construct Codex model: %w", ErrInvalidModel)
	}
	modelIDValue := agentmodel.ID(modelID)
	providerClient, err := factory(ctx, client, openaicodex.ChatModelConfig{Model: modelID, ReasoningEffort: "medium"})
	if err != nil {
		return nil, fmt.Errorf("construct Codex provider: %w", err)
	}
	codec, err := agentmodel.NewEinoJSONExtraStateCodec(agentmodel.EinoJSONExtraStateConfig{
		ExtraKey: ReasoningExtraKey,
		Contract: agentmodel.ProviderStateContract{
			CodecID: ReasoningCodecID, Version: ReasoningCodecVersion,
			CompatibilityKey: ReasoningCompatibilityKey, Limits: reasoningLimits(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("construct Codex state codec: %w", err)
	}
	streamer, err := agentmodel.NewEinoStreamerWithProviderState(providerClient, codec)
	if err != nil {
		return nil, fmt.Errorf("construct Codex streamer: %w", err)
	}
	return &resolver{modelID: modelIDValue, streamer: streamer}, nil
}

func (r *resolver) Resolve(_ context.Context, selection agentmodel.Selection, _ agentmodel.Runtime) (agentmodel.Resolved, error) {
	if r == nil || r.streamer == nil || selection.ProviderID != ProviderID || selection.ModelID != r.modelID || selection.Variant != "" {
		return agentmodel.Resolved{}, fmt.Errorf("resolve Codex model: %w", ErrInvalidModel)
	}
	provider := agentmodel.Provider{ID: ProviderID, Name: "OpenAI Codex", Source: "eino-providers/openaicodex"}
	descriptor := agentmodel.Descriptor{
		ID: r.modelID, ProviderID: ProviderID, Name: string(r.modelID), Family: "gpt",
		Capabilities: map[string]bool{"streaming": true},
	}
	return agentmodel.Resolved{Provider: provider, Model: descriptor, Streamer: r.streamer}, nil
}
