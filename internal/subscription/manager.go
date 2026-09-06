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
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

type Status uint8

const (
	NotLoggedIn Status = iota
	LoggedIn
	RefreshRequired
)

var ErrAuth = errors.New("Codex authentication failed")

var ErrCatalogUnavailable = errors.New("Codex model catalog is unavailable")

// CatalogCompatibilityVersion is the OpenAI Codex catalog compatibility
// baseline, not an eino-tui or Go module version.
const CatalogCompatibilityVersion = "0.153.2"

type authClient interface {
	LoginDevice(context.Context) (codexauth.Credentials, error)
	Status(context.Context) (codexauth.StatusInfo, error)
	HTTPClient(context.Context) (*http.Client, error)
	ListModels(context.Context, string) ([]codexauth.ModelCatalogEntry, error)
}

type Manager struct {
	client authClient
	gate   chan struct{}
}

type clientFactory func(codexauth.Options) authClient

var deviceCodePattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// New constructs the app-owned credential client with fully owned output sinks.
func New(deviceOutput io.Writer) *Manager {
	return newWithFactory(deviceOutput, func(options codexauth.Options) authClient {
		return codexauth.NewClient(options)
	})
}

func newWithFactory(deviceOutput io.Writer, factory clientFactory) *Manager {
	if deviceOutput == nil {
		deviceOutput = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := factory(codexauth.Options{
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
	return managerWithClient(client)
}

func newManager(client authClient) *Manager { return managerWithClient(client) }

func managerWithClient(client authClient) *Manager {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Manager{client: client, gate: gate}
}

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
		wrapped := *client
		transport := client.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		wrapped.Transport = gatedTransport{manager: m, transport: transport}
		return &wrapped, nil
	}
	if errors.Is(err, codexauth.ErrNotLoggedIn) {
		return nil, codexauth.ErrNotLoggedIn
	}
	return nil, ErrAuth
}

// ListModels fetches and normalizes the authenticated account catalog.
func (m *Manager) ListModels(ctx context.Context) ([]codexmodel.CatalogEntry, error) {
	if m == nil || m.client == nil || m.gate == nil {
		return nil, ErrCatalogUnavailable
	}
	release, err := m.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	remote, err := m.client.ListModels(ctx, CatalogCompatibilityVersion)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, ErrCatalogUnavailable
	}
	return codexmodel.NormalizeCatalog(remote), nil
}

func (m *Manager) acquire(ctx context.Context) (func(), error) {
	if m == nil || m.gate == nil {
		return nil, ErrCatalogUnavailable
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.gate:
		return func() { m.gate <- struct{}{} }, nil
	}
}

type gatedTransport struct {
	manager   *Manager
	transport http.RoundTripper
}

func (g gatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || g.manager == nil || g.transport == nil {
		return nil, ErrAuth
	}
	release, err := g.manager.acquire(req.Context())
	if err != nil {
		return nil, err
	}
	defer release()
	return g.transport.RoundTrip(req)
}

func validDeviceValue(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\t") || textsafe.Display(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || r == 0x7f {
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
