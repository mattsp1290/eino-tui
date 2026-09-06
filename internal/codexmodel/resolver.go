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
	ReasoningEffortOptionKey  = "openaicodex.reasoning_effort"
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
	client  *http.Client
	factory chatModelFactory
	codec   agentmodel.ProviderStateCodec
}

// NewResolver builds a resolver that creates one immutable provider model per run.
func NewResolver(client *http.Client) (agentmodel.Resolver, error) {
	return newResolver(client, openaicodex.NewChatModelWithHTTPClient)
}

func newResolver(client *http.Client, factory chatModelFactory) (agentmodel.Resolver, error) {
	if client == nil || factory == nil {
		return nil, fmt.Errorf("construct Codex model: %w", ErrInvalidModel)
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
	return &resolver{client: client, factory: factory, codec: codec}, nil
}

func (r *resolver) Resolve(ctx context.Context, selection agentmodel.Selection, runtime agentmodel.Runtime) (agentmodel.Resolved, error) {
	effort, hasEffort := runtime.Options[ReasoningEffortOptionKey]
	if r == nil || r.client == nil || r.factory == nil || r.codec == nil || ValidateSelection(selection) != nil || !hasEffort ||
		len(runtime.Options) != 1 || !ValidReasoningEffort(effort) {
		return agentmodel.Resolved{}, fmt.Errorf("resolve Codex model: %w", ErrInvalidModel)
	}
	providerClient, err := r.factory(ctx, r.client, openaicodex.ChatModelConfig{Model: string(selection.ModelID), ReasoningEffort: effort})
	if err != nil || providerClient == nil {
		return agentmodel.Resolved{}, fmt.Errorf("resolve Codex model: provider unavailable")
	}
	streamer, err := agentmodel.NewEinoStreamerWithProviderState(providerClient, r.codec)
	if err != nil {
		return agentmodel.Resolved{}, fmt.Errorf("resolve Codex model: provider unavailable")
	}
	provider := agentmodel.Provider{ID: ProviderID, Name: "OpenAI Codex", Source: "eino-providers/openaicodex"}
	descriptor := agentmodel.Descriptor{
		ID: selection.ModelID, ProviderID: ProviderID, Name: string(selection.ModelID), Family: "gpt",
		Capabilities: map[string]bool{"streaming": true}, Options: map[string]string{ReasoningEffortOptionKey: effort},
	}
	return agentmodel.Resolved{Provider: provider, Model: descriptor, Streamer: newSafeProviderStateStreamer(streamer)}, nil
}
