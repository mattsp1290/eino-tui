.PHONY: fmt-check vet test test-race build check-mod test-integration test-pty check

fmt-check:
	@test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test:
	go test ./internal/platform ./internal/textsafe ./internal/demomodel ./internal/codexmodel ./internal/subscription ./internal/runtimeui ./internal/app ./internal/cli

test-race:
	go test -race ./...

build:
	go build ./cmd/eino-tui

check-mod:
	go mod tidy -diff
	go mod verify
	@replacements="$$(go list -m -f '{{if .Replace}}{{.Path}}{{end}}' all)" && test -z "$$replacements"
	@deps="$$(go list -deps ./cmd/eino-tui)" && case "$$deps" in *github.com/mattsp1290/eino-tui/internal/demomodel*|*github.com/mattsp1290/eino-tui/internal/pty*) exit 1;; esac
	@test "$$(go list -m -f '{{.GoVersion}}')" = "1.26.3"
	@test "$$(go list -m -f '{{.Version}}' github.com/mattsp1290/eino-agent)" = "v0.3.1"
	@test "$$(go list -m -f '{{.Version}}' github.com/mattsp1290/codex-auth-go)" = "v0.3.0"
	@test "$$(go list -m -f '{{.Version}}' github.com/mattsp1290/eino-providers)" = "v0.0.0-20260606014731-3e0069d028bc"
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
