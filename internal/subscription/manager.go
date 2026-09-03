package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	codexauth "github.com/mattsp1290/codex-auth-go"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
)

type Status uint8

const (
	NotLoggedIn Status = iota
	LoggedIn
	RefreshRequired
)

var ErrAuth = errors.New("Codex authentication failed")

type authClient interface {
	LoginDevice(context.Context) (codexauth.Credentials, error)
	Status(context.Context) (codexauth.StatusInfo, error)
	HTTPClient(context.Context) (*http.Client, error)
}

type Manager struct{ client authClient }

var authClientFactory = func(options codexauth.Options) authClient {
	return codexauth.NewClient(options)
}

var deviceCodePattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// New constructs the app-owned credential client with fully owned output sinks.
func New(deviceOutput io.Writer) *Manager {
	if deviceOutput == nil {
		deviceOutput = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := authClientFactory(codexauth.Options{
		AppName: codexmodel.AppName,
		Logger:  logger,
		DevicePrompt: func(uri, code string) error {
			if !validDeviceURI(uri) || !validDeviceValue(code, 128) || !deviceCodePattern.MatchString(code) {
				return ErrAuth
			}
			_, err := fmt.Fprintf(deviceOutput, "Open %s and enter code: %s\n", uri, code)
			if err != nil {
				return ErrAuth
			}
			return nil
		},
	})
	return &Manager{client: client}
}

func newManager(client authClient) *Manager { return &Manager{client: client} }

func (m *Manager) LoginDevice(ctx context.Context) error {
	if m == nil || m.client == nil {
		return ErrAuth
	}
	_, err := m.client.LoginDevice(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ErrAuth
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	if m == nil || m.client == nil {
		return NotLoggedIn, ErrAuth
	}
	status, err := m.client.Status(ctx)
	if err != nil {
		return NotLoggedIn, ErrAuth
	}
	if !status.LoggedIn {
		return NotLoggedIn, nil
	}
	if status.Stale {
		return RefreshRequired, nil
	}
	return LoggedIn, nil
}

func (m *Manager) HTTPClient(ctx context.Context) (*http.Client, error) {
	if m == nil || m.client == nil {
		return nil, ErrAuth
	}
	client, err := m.client.HTTPClient(ctx)
	if err == nil {
		if client == nil {
			return nil, ErrAuth
		}
		return client, nil
	}
	if errors.Is(err, codexauth.ErrNotLoggedIn) {
		return nil, codexauth.ErrNotLoggedIn
	}
	return nil, ErrAuth
}

func validDeviceValue(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func validDeviceURI(value string) bool {
	if !validDeviceValue(value, 2048) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}
