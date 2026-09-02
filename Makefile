.PHONY: fmt-check vet test test-race build check-mod test-integration test-pty check

fmt-check:
	@test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test:
	go test ./internal/platform ./internal/textsafe ./internal/demomodel ./internal/runtimeui ./internal/app ./internal/cli

test-race:
	go test -race ./internal/platform ./internal/textsafe ./internal/demomodel ./internal/runtimeui ./internal/app ./internal/cli ./internal/integration

build:
	go build ./cmd/eino-tui

check-mod:
	go mod tidy
	git diff --exit-code -- go.mod go.sum
	go mod verify
	@! go list -m -json all | grep -q '"Replace"'
	@test "$$(go list -m -f '{{.GoVersion}}')" = "1.26.3"
	@test "$$(go list -m -f '{{.Version}}' github.com/mattsp1290/eino-agent)" = "v0.2.0"
	@test "$$(go list -m -f '{{.Version}}' github.com/cloudwego/eino)" = "v0.8.13"
	@test "$$(go list -m -f '{{.Version}}' charm.land/bubbletea/v2)" = "v2.0.9"
	@test "$$(go list -m -f '{{.Version}}' charm.land/bubbles/v2)" = "v2.2.1"
	@test "$$(go list -m -f '{{.Version}}' charm.land/lipgloss/v2)" = "v2.0.6"
	@test "$$(go list -m -f '{{.Version}}' github.com/charmbracelet/x/ansi)" = "v0.11.8"
	@test "$$(go list -m -f '{{.Version}}' github.com/google/uuid)" = "v1.6.0"
	@test "$$(go list -m -f '{{.Version}}' github.com/creack/pty)" = "v1.1.24"

test-integration:
	go test ./internal/integration

test-pty:
	go test ./internal/pty

check: fmt-check check-mod vet test test-integration test-race build test-pty
