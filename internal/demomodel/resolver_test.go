package demomodel

import (
	"context"
	"errors"
	"io"
	"testing"

	einoschema "github.com/cloudwego/eino/schema"
	"github.com/mattsp1290/eino-agent/model"
)

func TestResolverStreamsScriptedChunksThroughPublicAdapter(t *testing.T) {
	resolved, err := Resolver(func(context.Context) error { return nil }).Resolve(context.Background(), model.Selection{ProviderID: ProviderID, ModelID: ModelID}, model.Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := resolved.Streamer.StreamProvider(context.Background(), model.Request{Messages: []*einoschema.Message{einoschema.UserMessage("private prompt")}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var chunks []string
	for {
		delta, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, delta.Message.Content)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks = %#v", chunks)
	}
	for _, chunk := range chunks {
		if chunk == "private prompt" {
			t.Fatal("prompt echoed")
		}
	}
}

func TestResolverPacingHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	resolved, err := Resolver(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }).Resolve(ctx, model.Selection{ProviderID: ProviderID, ModelID: ModelID}, model.Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := resolved.Streamer.StreamProvider(ctx, model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	cancel()
	_, err = reader.Recv()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Recv error = %v", err)
	}
}
