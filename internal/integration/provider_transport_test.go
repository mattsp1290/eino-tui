package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type codexRequests struct {
	mu       sync.Mutex
	requests [][]byte
}

func (r *codexRequests) record(request *http.Request) (int, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, body)
	return len(r.requests), nil
}

func (r *codexRequests) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	requests := make([][]byte, len(r.requests))
	for i := range r.requests {
		requests[i] = append([]byte(nil), r.requests[i]...)
	}
	return requests
}

func (r *codexRequests) request(index int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.requests[index]...)
}

func (r *codexRequests) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

type scriptedCodexTransport struct {
	codexRequests
	responses [][]string
}

func (t *scriptedCodexTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	turn, err := t.record(request)
	if err != nil {
		return nil, err
	}
	if turn > len(t.responses) {
		return nil, fmt.Errorf("unexpected Codex fixture request %d", turn)
	}
	return codexSSEResponse(request, t.responses[turn-1]), nil
}

func codexSSEResponse(request *http.Request, events []string) *http.Response {
	var stream strings.Builder
	for _, event := range events {
		stream.WriteString("data: ")
		stream.WriteString(event)
		stream.WriteString("\n\n")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String())), Request: request}
}

func newFileReadToolTransport() *scriptedCodexTransport {
	return &scriptedCodexTransport{responses: [][]string{
		{
			`{"type":"response.output_text.delta","delta":"I’ll inspect it."}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"file_read","call_id":"call_read_1","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":"}`,
			`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"fixture.txt\"}"}`,
			`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"path\":\"fixture.txt\"}"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"file_read","call_id":"call_read_1","arguments":"{\"path\":\"fixture.txt\"}"}}`,
			`{"type":"response.completed","response":{"id":"response_tool_1"}}`,
		},
		{
			`{"type":"response.output_text.delta","delta":"The fixture says hello."}`,
			`{"type":"response.completed","response":{"id":"response_tool_2"}}`,
		},
	}}
}

func newPathToolTransport(path string) *scriptedCodexTransport {
	arguments, _ := json.Marshal(map[string]string{"path": path})
	quoted, _ := json.Marshal(string(arguments))
	return &scriptedCodexTransport{responses: [][]string{
		{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"file_read","call_id":"call_path","arguments":""}}`,
			`{"type":"response.function_call_arguments.done","output_index":0,"arguments":` + string(quoted) + `}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"file_read","call_id":"call_path","arguments":` + string(quoted) + `}}`,
			`{"type":"response.completed","response":{"id":"response_path_1"}}`,
		},
		{
			`{"type":"response.output_text.delta","delta":"The requested path is outside the workspace."}`,
			`{"type":"response.completed","response":{"id":"response_path_2"}}`,
		},
	}}
}

func providerToolNames(t *testing.T, tools []json.RawMessage) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		var definition struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &definition); err != nil || definition.Name == "" || len(definition.Parameters) == 0 {
			t.Fatal("invalid provider tool definition")
		}
		names = append(names, definition.Name)
	}
	return names
}
