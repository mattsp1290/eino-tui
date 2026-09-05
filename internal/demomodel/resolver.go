package demomodel

import (
	"context"
	"fmt"
	"time"

	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/providers/fake"
)

const fixtureReasoningOption = "openaicodex.reasoning_effort"

const (
	ProviderID = model.ProviderID("demo")
	ModelID    = model.ID("scripted-v1")
)

type Waiter func(context.Context) error

func TimerWait(duration time.Duration) Waiter {
	return func(ctx context.Context) error {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
}

// Resolver uses the public fake provider path, then adds observable pacing.
func Resolver(wait Waiter) model.Resolver {
	return resolverWithSteps(wait, []fake.Step{
		{Content: "Codex fixture response: "},
		{Content: "your message was received. "},
		{Content: "Deterministic test transport completed."},
	})
}

// ErrorResolver is used by the test-only PTY fixture to exercise redacted model failure.
func ErrorResolver(wait Waiter, failure error) model.Resolver {
	return resolverWithSteps(wait, []fake.Step{{Err: failure}})
}

// ScriptedResolver is a test seam for backpressure and chunk-boundary coverage.
func ScriptedResolver(wait Waiter, chunks []string) model.Resolver {
	steps := make([]fake.Step, len(chunks))
	for index, chunk := range chunks {
		steps[index] = fake.Step{Content: chunk}
	}
	return resolverWithSteps(wait, steps)
}

// DynamicResolver is the credential-free PTY seam for per-turn model options.
func DynamicResolver(wait Waiter) model.Resolver {
	return dynamicResolver(wait, func(selection model.Selection, runtime model.Runtime) []fake.Step {
		return []fake.Step{
			{Content: "Codex fixture response: selection " + string(selection.ModelID) + " · " + runtime.Options[fixtureReasoningOption] + ". "},
			{Content: "Your message was received. "},
			{Content: "Deterministic test transport completed."},
		}
	})
}

// DynamicErrorResolver returns a per-turn fixture streamer that fails safely.
func DynamicErrorResolver(wait Waiter, failure error) model.Resolver {
	return dynamicResolver(wait, func(model.Selection, model.Runtime) []fake.Step {
		return []fake.Step{{Err: failure}}
	})
}

// DynamicScriptedResolver is the per-turn equivalent of ScriptedResolver.
func DynamicScriptedResolver(wait Waiter, chunks []string) model.Resolver {
	return dynamicResolver(wait, func(model.Selection, model.Runtime) []fake.Step {
		steps := make([]fake.Step, len(chunks))
		for i, chunk := range chunks {
			steps[i] = fake.Step{Content: chunk}
		}
		return steps
	})
}

func dynamicResolver(wait Waiter, steps func(model.Selection, model.Runtime) []fake.Step) model.Resolver {
	if wait == nil {
		wait = TimerWait(50 * time.Millisecond)
	}
	return model.ResolverFunc(func(ctx context.Context, selection model.Selection, runtime model.Runtime) (model.Resolved, error) {
		effort := runtime.Options[fixtureReasoningOption]
		if selection.ProviderID == "" || selection.ModelID == "" || selection.Variant != "" || effort == "" {
			return model.Resolved{}, fmt.Errorf("invalid fixture selection")
		}
		provider := &fake.Provider{
			ID: selection.ProviderID, Name: "Credential-free dynamic fixture provider",
			Descriptors: []model.Descriptor{{
				ID: selection.ModelID, ProviderID: selection.ProviderID, Name: string(selection.ModelID),
				ContextLimit: 8192, OutputLimit: 512, Capabilities: map[string]bool{"streaming": true},
			}},
			Steps: steps(selection, runtime),
		}
		resolved, err := (model.AdapterResolver{Adapters: []model.Adapter{provider}}).Resolve(ctx, selection, runtime)
		if err != nil {
			return model.Resolved{}, err
		}
		resolved.Model.Options = map[string]string{fixtureReasoningOption: effort}
		resolved.Streamer = pacedStreamer{upstream: resolved.Streamer, wait: wait}
		return resolved, nil
	})
}

func resolverWithSteps(wait Waiter, steps []fake.Step) model.Resolver {
	if wait == nil {
		wait = TimerWait(50 * time.Millisecond)
	}
	provider := &fake.Provider{
		ID:   ProviderID,
		Name: "Credential-free demo provider",
		Descriptors: []model.Descriptor{{
			ID: ModelID, ProviderID: ProviderID, Name: "Scripted demo response",
			ContextLimit: 8192, OutputLimit: 512,
			Capabilities: map[string]bool{"streaming": true},
		}},
		Steps: steps,
	}
	base := model.AdapterResolver{Adapters: []model.Adapter{provider}}
	return model.ResolverFunc(func(ctx context.Context, selection model.Selection, runtime model.Runtime) (model.Resolved, error) {
		resolved, err := base.Resolve(ctx, selection, runtime)
		if err != nil {
			return model.Resolved{}, err
		}
		resolved.Streamer = pacedStreamer{upstream: resolved.Streamer, wait: wait}
		return resolved, nil
	})
}
