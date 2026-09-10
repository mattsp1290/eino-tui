// Package conversationtools owns the single native host tool that lets the
// model rename the current conversation. The executor uses only the writer the
// runtime binds to the current session, workspace, and execution fence.
package conversationtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	einoschema "github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/extension"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/tools"
	orderedmap "github.com/wk8/go-ordered-map/v2"

	"github.com/mattsp1290/eino-tui/internal/conversationnames"
)

const (
	// Name is the provider-visible tool name.
	Name = "rename_conversation"
	// Subject is the fixed activity subject; the title argument is never shown.
	Subject = "current conversation"

	componentInstance = "eino-tui-rename-conversation-tool"
	componentName     = "eino-tui-conversation-tools"
	componentVersion  = "v1"
	registrationID    = "rename-conversation"
	// registrationOrder sorts the rename tool after the read-only leaf tools,
	// which register at runtime.OrderApplication plus their catalog index.
	registrationOrder = 1000

	// These contract strings are the exact schema and executor behavior. Any
	// change to the schema, bounds, result shape, or retry policy must change
	// the matching string, which changes the composed tool identity.
	schemaContract   = "eino-tui/rename_conversation/schema/v1: object{title: string, required} additionalProperties=false maxTitleRunes=256 maxTitleBytes=1024 maxRawInputBytes=8192"
	executorContract = "eino-tui/rename_conversation/executor/v1: Call.SessionTitle.SetTitle(normalized title) -> {renamed:bool, changed:bool}; retrySafe=false; allowSessionTitle=true"
	artifactContract = "eino-tui/conversation-tools/artifact/v1"
	configContract   = "eino-tui/conversation-tools/config/v1: rename_conversation"
)

var (
	// ErrTitleWriterUnavailable reports an execution without the bound writer.
	ErrTitleWriterUnavailable = errors.New("conversation title writer unavailable")
	// ErrInputTooLarge reports raw arguments over the decoding bound.
	ErrInputTooLarge = errors.New("rename input too large")
	// ErrRenameFailed is the content-free executor failure.
	ErrRenameFailed = errors.New("conversation rename failed")
)

type renameInput struct {
	Title string `json:"title"`
}

type renameResult struct {
	Renamed bool `json:"renamed"`
	Changed bool `json:"changed"`
}

// SchemaHash is the immutable identity of the model-facing contract.
func SchemaHash() string { return contractHash(schemaContract) }

// ExecutorHash is the immutable identity of the executor contract.
func ExecutorHash() string { return contractHash(executorContract) }

func contractHash(contract string) string {
	digest := sha256.Sum256([]byte(contract))
	return hex.EncodeToString(digest[:])
}

// Definition is the exact JSON-native declaration registered by Mount.
func Definition() tools.Definition {
	return tools.Definition{
		Name:              Name,
		Description:       "Rename the current conversation. Supply only the new title; the conversation is always the one you are in.",
		Parameters:        parameters(),
		Normalize:         normalize,
		Execute:           execute,
		RetrySafe:         false,
		AllowSessionTitle: true,
		Retention:         agentruntime.RetentionPolicy{MaxInlineBytes: 256},
	}
}

func parameters() *einoschema.ParamsOneOf {
	properties := orderedmap.New[string, *jsonschema.Schema]()
	properties.Set("title", &jsonschema.Schema{Type: "string", Description: "The new conversation title.", MinLength: uint64Ptr(1)})
	return einoschema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             []string{"title"},
		AdditionalProperties: jsonschema.FalseSchema,
	})
}

func uint64Ptr(value uint64) *uint64 { return &value }

// Mount registers the tool through the public composition registry.
func Mount(ctx context.Context, registry *composition.Registry) (*composition.Mount, error) {
	identity, err := composition.NewToolSourceIdentity(SchemaHash(), ExecutorHash())
	if err != nil {
		return nil, fmt.Errorf("conversation tool identity: %w", err)
	}
	component := extension.Component{
		InstanceID: componentInstance,
		Artifact: extension.Artifact{
			Name: componentName, Version: componentVersion,
			Hash: contractHash(artifactContract), ConfigHash: contractHash(configContract), SourceKind: extension.SourceNative,
		},
	}
	return registry.Mount(ctx, component, composition.InstallerFunc(func(_ context.Context, registrar *composition.Registrar) error {
		return registrar.Tool(composition.ToolRegistration{
			ID: registrationID, Order: agentruntime.OrderApplication + registrationOrder, Scope: extension.GlobalScope(), SourceIdentity: identity, Definition: Definition(),
		})
	}))
}

// normalize bounds and canonicalizes model input before durable storage. It
// rejects oversized raw arguments, unknown fields, and invalid titles without
// echoing values.
func normalize(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	input, err := decode(raw)
	if err != nil {
		return nil, err
	}
	title, err := conversationnames.NormalizeTitle(input.Title)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", tools.ErrMalformedInput, err)
	}
	return json.Marshal(renameInput{Title: title})
}

func decode(raw json.RawMessage) (renameInput, error) {
	if len(raw) > conversationnames.MaxRawToolInputBytes {
		return renameInput{}, fmt.Errorf("%w: %v", tools.ErrMalformedInput, ErrInputTooLarge)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input renameInput
	if err := decoder.Decode(&input); err != nil {
		return renameInput{}, fmt.Errorf("%w: rename arguments", tools.ErrMalformedInput)
	}
	if decoder.More() {
		return renameInput{}, fmt.Errorf("%w: trailing rename arguments", tools.ErrMalformedInput)
	}
	return input, nil
}

// execute changes only the current conversation's title through the bound
// writer. It does not retain the writer and accepts no target selectors.
func execute(ctx context.Context, execution tools.Execution) (json.RawMessage, error) {
	input, err := decode(execution.Input)
	if err != nil {
		return nil, err
	}
	title, err := conversationnames.NormalizeTitle(input.Title)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", tools.ErrMalformedInput, err)
	}
	writer := execution.Call.SessionTitle
	if writer == nil {
		return nil, ErrTitleWriterUnavailable
	}
	result, err := writer.SetTitle(ctx, title)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, ErrRenameFailed
	}
	return json.Marshal(renameResult{Renamed: true, Changed: result.Changed})
}
