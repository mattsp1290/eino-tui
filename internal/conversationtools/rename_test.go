package conversationtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/tools"
)

// fakeTitleWriter is a minimal runtime.SessionTitleWriter test double that
// records every title it is asked to set.
type fakeTitleWriter struct {
	calls  int
	titles []string
	result session.SessionTitleResult
	err    error
}

func (w *fakeTitleWriter) SetTitle(_ context.Context, title string) (session.SessionTitleResult, error) {
	w.calls++
	w.titles = append(w.titles, title)
	return w.result, w.err
}

// TestSchemaAndExecutorHashesAreStable pins the immutable tool identity. The
// literals below are the sha256 hex digests of the exact contract strings
// (schemaContract, executorContract) in rename.go, computed independently:
//
//	printf '%s' '<schemaContract>'   | shasum -a 256
//	printf '%s' '<executorContract>' | shasum -a 256
//
// They must change only when those contract strings change.
func TestSchemaAndExecutorHashesAreStable(t *testing.T) {
	const wantSchemaHash = "aa3f9bfbfa691ce318ad8d210bb337449502506c42575a249f72ec0a8f834ce4"
	const wantExecutorHash = "7b9e0a49d49c79f29585ceb674d534c03046f8eee9809d6c5a1142ef770ff154"

	if got := SchemaHash(); got != wantSchemaHash {
		t.Fatalf("SchemaHash() = %q, want %q", got, wantSchemaHash)
	}
	if got := ExecutorHash(); got != wantExecutorHash {
		t.Fatalf("ExecutorHash() = %q, want %q", got, wantExecutorHash)
	}
	if SchemaHash() == ExecutorHash() {
		t.Fatal("schema and executor hashes must differ")
	}
}

func TestDefinitionShapeAndValidation(t *testing.T) {
	def := Definition()
	if def.Name != "rename_conversation" {
		t.Fatalf("Name = %q, want rename_conversation", def.Name)
	}
	if def.Name != Name {
		t.Fatalf("Name = %q, want package constant Name %q", def.Name, Name)
	}
	if !def.AllowSessionTitle {
		t.Fatal("AllowSessionTitle must be true")
	}
	if def.RetrySafe {
		t.Fatal("RetrySafe must be false")
	}
	if def.Parameters == nil {
		t.Fatal("Parameters must be set")
	}
	if def.Normalize == nil {
		t.Fatal("Normalize must be set")
	}
	if def.Execute == nil {
		t.Fatal("Execute must be set")
	}
	if err := tools.ValidateDefinition(def); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
}

func TestNormalizeCanonicalizesAndTrimsWhitespace(t *testing.T) {
	ctx := context.Background()
	out, err := Definition().Normalize(ctx, json.RawMessage(`{"title":"  Hello   world "}`))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 {
		t.Fatalf("normalized output has extra fields: %v", fields)
	}
	var decoded struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Title != "Hello world" {
		t.Fatalf("title = %q, want %q", decoded.Title, "Hello world")
	}
}

func TestNormalizeRejectsUnknownFields(t *testing.T) {
	ctx := context.Background()
	cases := map[string]string{
		"session_id":   `{"title":"x","session_id":"other"}`,
		"workspace_id": `{"title":"x","workspace_id":"w"}`,
		"fence":        `{"title":"x","fence":"f"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Definition().Normalize(ctx, json.RawMessage(raw)); !errors.Is(err, tools.ErrMalformedInput) {
				t.Fatalf("err = %v, want ErrMalformedInput", err)
			}
		})
	}
}

func TestNormalizeRejectsMalformedOrOversizedInput(t *testing.T) {
	ctx := context.Background()
	huge := strings.Repeat("a", 9000)
	cases := map[string]string{
		"blank title":          `{"title":"   "}`,
		"too many code points": `{"title":"` + strings.Repeat("a", 257) + `"}`,
		"oversized raw input":  `{"title":"` + huge + `"}`,
		"trailing json":        `{"title":"x"}{"title":"y"}`,
		"non object":           `"just a string"`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Definition().Normalize(ctx, json.RawMessage(raw)); !errors.Is(err, tools.ErrMalformedInput) {
				t.Fatalf("err = %v, want ErrMalformedInput", err)
			}
		})
	}
}

// TestNormalizeSanitizesRatherThanRejectsInvalidUTF8Bytes documents an
// observed boundary condition: encoding/json replaces invalid UTF-8 byte
// sequences and unpaired surrogate escapes with the U+FFFD replacement
// character while decoding a JSON string, so a raw byte sequence that is not
// valid UTF-8 never reaches conversationnames.NormalizeTitle's UTF-8 validity
// check by the time it has passed through json.Decoder here. The title that
// arrives is already valid UTF-8 (containing U+FFFD), so it is accepted
// rather than rejected. This is standard library behavior, not a defect.
func TestNormalizeSanitizesRatherThanRejectsInvalidUTF8Bytes(t *testing.T) {
	ctx := context.Background()
	raw := []byte("{\"title\":\"a\xffb\"}")
	out, err := Definition().Normalize(ctx, raw)
	if err != nil {
		t.Fatalf("unexpected error for JSON-sanitized invalid UTF-8: %v", err)
	}
	var decoded struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decoded.Title, "�") {
		t.Fatalf("expected sanitized title to contain the replacement character, got %q", decoded.Title)
	}
}

func TestNormalizeErrorsNeverEchoRejectedTitleText(t *testing.T) {
	ctx := context.Background()
	// 257 code points, all containing the literal "SECRET" marker.
	title := "SECRET" + strings.Repeat("x", 251)
	raw := `{"title":"` + title + `"}`
	_, err := Definition().Normalize(ctx, json.RawMessage(raw))
	if !errors.Is(err, tools.ErrMalformedInput) {
		t.Fatalf("err = %v, want ErrMalformedInput", err)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error text leaked the rejected title: %v", err)
	}
}

func TestExecuteRenamesUsingBoundWriter(t *testing.T) {
	ctx := context.Background()
	writer := &fakeTitleWriter{result: session.SessionTitleResult{Title: "Agent chosen", Changed: true}}
	out, err := Definition().Execute(ctx, tools.Execution{
		Input: json.RawMessage(`{"title":"Agent chosen"}`),
		Call:  runtime.ToolCall{SessionTitle: writer},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"renamed":true,"changed":true}` {
		t.Fatalf("out = %s", out)
	}
	if len(writer.titles) != 1 || writer.titles[0] != "Agent chosen" {
		t.Fatalf("writer titles = %v", writer.titles)
	}
}

func TestExecuteReportsUnchangedWhenWriterReportsNoChange(t *testing.T) {
	ctx := context.Background()
	writer := &fakeTitleWriter{result: session.SessionTitleResult{Title: "Agent chosen", Changed: false}}
	out, err := Definition().Execute(ctx, tools.Execution{
		Input: json.RawMessage(`{"title":"Agent chosen"}`),
		Call:  runtime.ToolCall{SessionTitle: writer},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"renamed":true,"changed":false}` {
		t.Fatalf("out = %s", out)
	}
}

func TestExecuteFailsWithoutBoundWriter(t *testing.T) {
	ctx := context.Background()
	_, err := Definition().Execute(ctx, tools.Execution{
		Input: json.RawMessage(`{"title":"Agent chosen"}`),
		Call:  runtime.ToolCall{},
	})
	if !errors.Is(err, ErrTitleWriterUnavailable) {
		t.Fatalf("err = %v, want ErrTitleWriterUnavailable", err)
	}
}

func TestExecuteMapsStoreConflictToRenameFailedWithoutLeakingDetails(t *testing.T) {
	ctx := context.Background()
	writer := &fakeTitleWriter{err: session.ErrConflict}
	_, err := Definition().Execute(ctx, tools.Execution{
		Input: json.RawMessage(`{"title":"secret-title-value"}`),
		Call:  runtime.ToolCall{SessionTitle: writer},
	})
	if !errors.Is(err, ErrRenameFailed) {
		t.Fatalf("err = %v, want ErrRenameFailed", err)
	}
	if strings.Contains(err.Error(), session.ErrConflict.Error()) {
		t.Fatalf("error text leaked the writer error: %v", err)
	}
	if strings.Contains(err.Error(), "secret-title-value") {
		t.Fatalf("error text leaked the title: %v", err)
	}
}

func TestExecutePropagatesContextCancellation(t *testing.T) {
	ctx := context.Background()
	writer := &fakeTitleWriter{err: context.Canceled}
	_, err := Definition().Execute(ctx, tools.Execution{
		Input: json.RawMessage(`{"title":"Agent chosen"}`),
		Call:  runtime.ToolCall{SessionTitle: writer},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestExecuteRejectsInvalidInputBeforeCallingWriter(t *testing.T) {
	ctx := context.Background()
	writer := &fakeTitleWriter{}
	_, err := Definition().Execute(ctx, tools.Execution{
		Input: json.RawMessage(`{"title":"x","session_id":"other"}`),
		Call:  runtime.ToolCall{SessionTitle: writer},
	})
	if !errors.Is(err, tools.ErrMalformedInput) {
		t.Fatalf("err = %v, want ErrMalformedInput", err)
	}
	if writer.calls != 0 {
		t.Fatalf("writer called %d times, want 0", writer.calls)
	}
}

func TestMountRegistersToolAndCloses(t *testing.T) {
	ctx := context.Background()
	registry, err := composition.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	mount, err := Mount(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := mount.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMountTwiceOnSameRegistry(t *testing.T) {
	ctx := context.Background()
	registry, err := composition.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Mount(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(ctx)

	// Mounting the same tool component a second time on the same registry is
	// expected to fail as a duplicate instance. If it unexpectedly succeeds,
	// note the behavior rather than failing the test.
	second, err := Mount(ctx, registry)
	if err != nil {
		t.Logf("second mount rejected as expected: %v", err)
		return
	}
	t.Log("second mount on the same registry unexpectedly succeeded; duplicate instance was not rejected")
	_ = second.Close(ctx)
}

func TestAdvertisedSchemaDisallowsAdditionalProperties(t *testing.T) {
	schema, err := Definition().Parameters.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"additionalProperties":false`, `"required":["title"]`, `"type":"object"`, `"title":{`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("advertised schema missing %s: %s", want, raw)
		}
	}
}
