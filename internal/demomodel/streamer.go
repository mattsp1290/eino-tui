package demomodel

import (
	"context"
	"errors"
	"io"

	einoschema "github.com/cloudwego/eino/schema"
	"github.com/mattsp1290/eino-agent/model"
)

type pacedStreamer struct {
	upstream model.Streamer
	wait     Waiter
}

func (s pacedStreamer) StreamProvider(ctx context.Context, request model.Request) (*einoschema.StreamReader[model.StreamDelta], error) {
	upstream, err := s.upstream.StreamProvider(ctx, request)
	if err != nil {
		return nil, err
	}
	reader, writer := einoschema.Pipe[model.StreamDelta](1)
	go func() {
		defer writer.Close()
		defer upstream.Close()
		for {
			delta, recvErr := upstream.Recv()
			if errors.Is(recvErr, io.EOF) {
				return
			}
			if recvErr != nil {
				writer.Send(model.StreamDelta{}, recvErr)
				return
			}
			if waitErr := s.wait(ctx); waitErr != nil {
				writer.Send(model.StreamDelta{}, waitErr)
				return
			}
			// eino-agent v0.3.1 treats all Eino Extra fields as provider-private.
			// Deterministic fixtures have no private state, so discard fake metadata.
			if delta.Message != nil {
				delta.Message.Extra = nil
			}
			if writer.Send(delta, nil) {
				return
			}
		}
	}()
	return reader, nil
}
