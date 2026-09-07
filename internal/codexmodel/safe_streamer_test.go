package codexmodel

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	codexauth "github.com/mattsp1290/codex-auth-go"
	agentmodel "github.com/mattsp1290/eino-agent/model"
)

type fakeProviderStateStreamer struct {
	reader     *schema.StreamReader[agentmodel.StreamDelta]
	streamErr  error
	contract   agentmodel.ProviderStateContract
	capture    agentmodel.ProviderStateCapture
	captureErr error
}

func (f *fakeProviderStateStreamer) StreamProvider(context.Context, agentmodel.Request) (*schema.StreamReader[agentmodel.StreamDelta], error) {
	return f.reader, f.streamErr
}

func (f *fakeProviderStateStreamer) ProviderStateContract() agentmodel.ProviderStateContract {
	return f.contract
}

func (f *fakeProviderStateStreamer) CaptureProviderState(*schema.Message) (agentmodel.ProviderStateCapture, error) {
	return f.capture, f.captureErr
}

func TestSafeProviderErrorClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   error
		code string
		text string
		is   error
	}{
		{name: "canceled", in: context.Canceled, text: context.Canceled.Error(), is: context.Canceled},
		{name: "deadline", in: context.DeadlineExceeded, text: context.DeadlineExceeded.Error(), is: context.DeadlineExceeded},
		{name: "plan", in: errors.Join(errors.New("secret"), codexauth.ErrPlanNotIncluded), code: "codex_plan_unavailable", text: "Codex plan unavailable", is: codexauth.ErrPlanNotIncluded},
		{name: "quota", in: errors.Join(errors.New("secret"), codexauth.ErrQuotaExceeded), code: "codex_quota_exceeded", text: "Codex quota exceeded", is: codexauth.ErrQuotaExceeded},
		{name: "state invalid", in: errors.Join(errors.New("secret"), agentmodel.ErrProviderStateInvalid), code: "codex_provider_state_invalid", text: "Codex provider state invalid", is: agentmodel.ErrProviderStateInvalid},
		{name: "state large", in: errors.Join(errors.New("secret"), agentmodel.ErrProviderStateTooLarge), code: "codex_provider_state_too_large", text: "Codex provider state too large", is: agentmodel.ErrProviderStateTooLarge},
		{name: "state mismatch", in: errors.Join(errors.New("secret"), agentmodel.ErrProviderStateMismatch), code: "codex_provider_state_mismatch", text: "Codex provider state mismatch", is: agentmodel.ErrProviderStateMismatch},
		{name: "state version", in: errors.Join(errors.New("secret"), agentmodel.ErrProviderStateVersion), code: "codex_provider_state_version", text: "Codex provider state version unsupported", is: agentmodel.ErrProviderStateVersion},
		{name: "unknown", in: errors.New("secret provider failure"), code: "codex_provider_failed", text: "Codex provider request failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := safeProviderError(test.in)
			if err == nil || err.Error() != test.text || strings.Contains(err.Error(), "secret") {
				t.Fatalf("safeProviderError(%v) = %v", test.in, err)
			}
			if test.is != nil && !errors.Is(err, test.is) {
				t.Fatalf("error %v does not preserve %v", err, test.is)
			}
			if test.code != "" {
				var providerErr agentmodel.Error
				if !errors.As(err, &providerErr) || providerErr.Code != test.code {
					t.Fatalf("typed error = %#v, want code %q", providerErr, test.code)
				}
			}
		})
	}
}

func TestSafeProviderStateStreamerSanitizesImmediateAndReceiveErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		immediate bool
	}{
		{name: "immediate", immediate: true},
		{name: "receive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			const secret = "secret-provider-error"
			upstream := &fakeProviderStateStreamer{}
			if test.immediate {
				upstream.streamErr = errors.New(secret)
			} else {
				reader, writer := schema.Pipe[agentmodel.StreamDelta](1)
				writer.Send(agentmodel.StreamDelta{}, errors.New(secret))
				writer.Close()
				upstream.reader = reader
			}
			stream, err := newSafeProviderStateStreamer(upstream).StreamProvider(context.Background(), agentmodel.Request{})
			if !test.immediate {
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				_, err = stream.Recv()
			}
			if err == nil || err.Error() != "Codex provider request failed" || strings.Contains(err.Error(), secret) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSafeProviderStateStreamerPassesToolCalls(t *testing.T) {
	t.Parallel()
	const secret = "secret_tool_name"
	reader := schema.StreamReaderFromArray([]agentmodel.StreamDelta{{
		Message: schema.AssistantMessage("", []schema.ToolCall{{
			ID: "call", Type: "function", Function: schema.FunctionCall{Name: secret, Arguments: `{"secret":"value"}`},
		}}),
	}})
	stream, err := newSafeProviderStateStreamer(&fakeProviderStateStreamer{reader: reader}).StreamProvider(context.Background(), agentmodel.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	delta, err := stream.Recv()
	if err != nil || delta.Message == nil || len(delta.Message.ToolCalls) != 1 || delta.Message.ToolCalls[0].Function.Name != secret || delta.Message.ToolCalls[0].Function.Arguments != `{"secret":"value"}` {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
}

func TestSafeProviderStateStreamerPassesDeltasStateAndClose(t *testing.T) {
	t.Parallel()
	reader, writer := schema.Pipe[agentmodel.StreamDelta](1)
	wantDelta := agentmodel.StreamDelta{Message: schema.AssistantMessage("safe", nil), Usage: agentmodel.Usage{InputTokens: 3}}
	writer.Send(wantDelta, nil)
	wantContract := agentmodel.ProviderStateContract{CodecID: "codec", Version: 1, CompatibilityKey: "compat"}
	wantCapture := agentmodel.ProviderStateCapture{ClaimedKeys: []string{"owned"}}
	upstream := &fakeProviderStateStreamer{reader: reader, contract: wantContract, capture: wantCapture}
	streamer := newSafeProviderStateStreamer(upstream)
	stream, err := streamer.StreamProvider(context.Background(), agentmodel.Request{})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := stream.Recv()
	if err != nil || delta.Message == nil || delta.Message.Content != wantDelta.Message.Content || delta.Usage != wantDelta.Usage {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
	if stateful := streamer.(agentmodel.ProviderStateStreamer); stateful.ProviderStateContract() != wantContract {
		t.Fatalf("contract = %#v", stateful.ProviderStateContract())
	} else if capture, captureErr := stateful.CaptureProviderState(schema.AssistantMessage("safe", nil)); captureErr != nil || len(capture.ClaimedKeys) != 1 || capture.ClaimedKeys[0] != "owned" {
		t.Fatalf("capture = %#v, error = %v", capture, captureErr)
	}
	stream.Close()
	if closed := writer.Send(agentmodel.StreamDelta{}, io.EOF); !closed {
		t.Fatal("closing safe stream did not close the upstream reader")
	}
}

func TestSafeProviderStateStreamerRejectsNilStream(t *testing.T) {
	t.Parallel()
	stream, err := newSafeProviderStateStreamer(&fakeProviderStateStreamer{}).StreamProvider(context.Background(), agentmodel.Request{})
	if stream != nil || err == nil || err.Error() != "Codex provider request failed" {
		t.Fatalf("stream=%v error=%v", stream, err)
	}
}
