package codexmodel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-providers/openaicodex"
)

type inertChatModel struct{}

func (inertChatModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	return schema.AssistantMessage("", nil), nil
}

type countingChatModel struct{ calls int }

type blockingTransport struct{ entered chan struct{} }

func (t blockingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	close(t.entered)
	<-request.Context().Done()
	return nil, request.Context().Err()
}

type responseRecordingTransport struct{ request []byte }

func reasoningRuntime(effort string) agentmodel.Runtime {
	return agentmodel.Runtime{Options: map[string]string{ReasoningEffortOptionKey: effort}}
}

func (t *responseRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	t.request = append([]byte(nil), body...)
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{}}`,
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		`data: {"type":"response.output_text.delta","delta":", world"}`,
		`data: {"type":"response.completed","response":{"id":"response","usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14}}}`,
		"",
	}, "\n\n")
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: request}, nil
}

func (m *countingChatModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	m.calls++
	return schema.AssistantMessage("", nil), nil
}
func (m *countingChatModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.calls++
	return nil, errors.New("unexpected provider dispatch")
}
func (m *countingChatModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	m.calls++
	return m, nil
}
func (inertChatModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	reader, writer := schema.Pipe[*schema.Message](1)
	writer.Send(schema.AssistantMessage("", nil), nil)
	writer.Close()
	return reader, nil
}
func (inertChatModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return inertChatModel{}, nil
}

func TestResolverUsesAuthenticatedClientAndExactStateContract(t *testing.T) {
	httpClient := &http.Client{}
	var capturedClient *http.Client
	var capturedConfig openaicodex.ChatModelConfig
	resolver, err := newResolver(httpClient, func(_ context.Context, client *http.Client, cfg openaicodex.ChatModelConfig) (einomodel.ToolCallingChatModel, error) {
		capturedClient, capturedConfig = client, cfg
		return inertChatModel{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := agentmodel.Selection{ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)}
	resolved, err := resolver.Resolve(context.Background(), selection, reasoningRuntime(ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	if capturedClient != httpClient || capturedConfig.Model != DefaultModel || capturedConfig.ReasoningEffort != "medium" || capturedConfig.DisableReasoning || capturedConfig.HTTPClient != nil || capturedConfig.AppName != "" {
		t.Fatalf("provider config/client = %p %#v", capturedClient, capturedConfig)
	}
	if resolved.Provider.ID != ProviderID || resolved.Model.ID != agentmodel.ID(DefaultModel) || resolved.Model.ProviderID != ProviderID || !resolved.Model.Capabilities["streaming"] {
		t.Fatalf("resolved descriptor = %#v", resolved)
	}
	stateful, ok := resolved.Streamer.(agentmodel.ProviderStateStreamer)
	if !ok {
		t.Fatalf("streamer is %T, not state-aware", resolved.Streamer)
	}
	contract := stateful.ProviderStateContract()
	if contract.CodecID != ReasoningCodecID || contract.Version != ReasoningCodecVersion || contract.CompatibilityKey != ReasoningCompatibilityKey || contract.Limits != reasoningLimits() {
		t.Fatalf("state contract = %#v", contract)
	}
}

func TestResolverBuildsFreshImmutableModelForEveryAcceptedPair(t *testing.T) {
	client := &http.Client{}
	var captured []openaicodex.ChatModelConfig
	resolver, err := newResolver(client, func(_ context.Context, got *http.Client, cfg openaicodex.ChatModelConfig) (einomodel.ToolCallingChatModel, error) {
		if got != client {
			t.Fatalf("client=%p want=%p", got, client)
		}
		captured = append(captured, cfg)
		return inertChatModel{}, nil
	})
	if err != nil || len(captured) != 0 {
		t.Fatalf("construction err=%v provider builds=%d", err, len(captured))
	}
	pairs := []struct{ model, effort string }{
		{DefaultModel, ReasoningEffortLow}, {"o4-live", ReasoningEffortMedium}, {DefaultModel, ReasoningEffortHigh},
	}
	var prior agentmodel.Resolved
	for i, pair := range pairs {
		resolved, resolveErr := resolver.Resolve(context.Background(), agentmodel.Selection{ProviderID: ProviderID, ModelID: agentmodel.ID(pair.model)}, reasoningRuntime(pair.effort))
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		if resolved.Model.ID != agentmodel.ID(pair.model) || resolved.Model.Options[ReasoningEffortOptionKey] != pair.effort || captured[i].Model != pair.model || captured[i].ReasoningEffort != pair.effort {
			t.Fatalf("pair %d resolved=%#v captured=%#v", i, resolved.Model, captured[i])
		}
		if i > 0 && prior.Model.Options[ReasoningEffortOptionKey] != pairs[i-1].effort {
			t.Fatalf("prior resolution mutated: %#v", prior.Model)
		}
		prior = resolved
	}
}

func TestResolverRejectsInvalidRuntimeBeforeFactoryAndRedactsFactoryFailures(t *testing.T) {
	calls := 0
	resolver, err := newResolver(&http.Client{}, func(context.Context, *http.Client, openaicodex.ChatModelConfig) (einomodel.ToolCallingChatModel, error) {
		calls++
		return nil, errors.New("TOKEN /secret")
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := agentmodel.Selection{ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)}
	for _, runtime := range []agentmodel.Runtime{
		{}, reasoningRuntime("xhigh"),
		{Options: map[string]string{ReasoningEffortOptionKey: ReasoningEffortLow, "canary": "TOKEN"}},
	} {
		if _, err := resolver.Resolve(context.Background(), selection, runtime); err == nil {
			t.Fatalf("runtime accepted: %#v", runtime)
		}
	}
	if calls != 0 {
		t.Fatalf("factory calls before valid input=%d", calls)
	}
	_, err = resolver.Resolve(context.Background(), selection, reasoningRuntime(ReasoningEffortLow))
	if err == nil || strings.Contains(err.Error(), "TOKEN") || strings.Contains(err.Error(), "/secret") || calls != 1 {
		t.Fatalf("factory error=%v calls=%d", err, calls)
	}
	if _, err := newResolver(nil, nil); err == nil {
		t.Fatal("nil construction accepted")
	}
}

func TestResolverRejectsSelectionDrift(t *testing.T) {
	resolver, err := newResolver(&http.Client{}, func(context.Context, *http.Client, openaicodex.ChatModelConfig) (einomodel.ToolCallingChatModel, error) {
		return inertChatModel{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []agentmodel.Selection{
		{ProviderID: "other", ModelID: agentmodel.ID(DefaultModel)},
		{ProviderID: ProviderID, ModelID: "bad model"},
		{ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel), Variant: "unsafe"},
	} {
		if _, err := resolver.Resolve(context.Background(), selection, reasoningRuntime(ReasoningEffortMedium)); err == nil {
			t.Fatalf("selection accepted: %#v", selection)
		}
	}
}

func TestStateMismatchFailsBeforeProviderDispatch(t *testing.T) {
	client := &countingChatModel{}
	resolver, err := newResolver(&http.Client{}, func(context.Context, *http.Client, openaicodex.ChatModelConfig) (einomodel.ToolCallingChatModel, error) {
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), agentmodel.Selection{ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)}, reasoningRuntime(ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolved.Streamer.StreamProvider(context.Background(), agentmodel.Request{
		Identity: agentmodel.Identity{SessionID: "active-session", ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)},
		Messages: []*schema.Message{schema.AssistantMessage("previous", nil)},
		ProviderState: []agentmodel.ProviderMessageState{{
			MessageIndex: 0, MessageID: "message", SourceSessionID: "wrong-session", SourceRunID: "run",
			ProviderID: string(ProviderID), SourceModelID: DefaultModel, CodecID: ReasoningCodecID,
			Version: ReasoningCodecVersion, CompatibilityKey: ReasoningCompatibilityKey,
			Items: []agentmodel.ProviderStateItem{{Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"SECRET"}`)}},
		}},
	})
	if !errors.Is(err, agentmodel.ErrProviderStateMismatch) || client.calls != 0 {
		t.Fatalf("error=%v provider calls=%d", err, client.calls)
	}
}

func TestRealProviderTransportHonorsCancellation(t *testing.T) {
	entered := make(chan struct{})
	resolver, err := NewResolver(&http.Client{Transport: blockingTransport{entered: entered}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), agentmodel.Selection{ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)}, reasoningRuntime(ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, streamErr := resolved.Streamer.StreamProvider(ctx, agentmodel.Request{
			Identity: agentmodel.Identity{SessionID: "session", RunID: "run", AssistantMessageID: "message", ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)},
			Messages: []*schema.Message{schema.UserMessage("cancel safely")},
		})
		done <- streamErr
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestRealProviderStreamsTextUsageAndNoTools(t *testing.T) {
	transport := &responseRecordingTransport{}
	resolver, err := NewResolver(&http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), agentmodel.Selection{ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)}, reasoningRuntime(ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := resolved.Streamer.StreamProvider(context.Background(), agentmodel.Request{
		Identity: agentmodel.Identity{SessionID: "session", RunID: "run", AssistantMessageID: "message", ProviderID: ProviderID, ModelID: agentmodel.ID(DefaultModel)},
		Messages: []*schema.Message{schema.UserMessage("hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var content strings.Builder
	var usage agentmodel.Usage
	updates := 0
	for {
		delta, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		updates++
		if delta.Message != nil {
			content.WriteString(delta.Message.Content)
		}
		if delta.Usage != (agentmodel.Usage{}) {
			usage = delta.Usage
		}
	}
	if updates < 3 || content.String() != "Hello, world" || usage.InputTokens != 11 || usage.OutputTokens != 3 {
		t.Fatalf("updates=%d content=%q usage=%#v", updates, content.String(), usage)
	}
	var request struct {
		Tools     []json.RawMessage `json:"tools"`
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		Include []string `json:"include"`
	}
	if err := json.Unmarshal(transport.request, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 0 || request.Reasoning.Effort != "medium" || len(request.Include) != 1 || request.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("request = %s", transport.request)
	}
}
