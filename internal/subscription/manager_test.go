package subscription

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	codexauth "github.com/mattsp1290/codex-auth-go"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
)

type managerRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn managerRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

type fakeAuthClient struct {
	loginErr  error
	status    codexauth.StatusInfo
	statusErr error
	http      *http.Client
	httpErr   error
	models    []codexauth.ModelCatalogEntry
	modelsErr error
	version   string
	list      func(context.Context, string) ([]codexauth.ModelCatalogEntry, error)
}

func (f *fakeAuthClient) LoginDevice(context.Context) (codexauth.Credentials, error) {
	return codexauth.Credentials{Access: "TOKEN-SHOULD-NOT-ESCAPE"}, f.loginErr
}
func (f *fakeAuthClient) Status(context.Context) (codexauth.StatusInfo, error) {
	return f.status, f.statusErr
}
func (f *fakeAuthClient) HTTPClient(context.Context) (*http.Client, error) {
	return f.http, f.httpErr
}
func (f *fakeAuthClient) ListModels(ctx context.Context, version string) ([]codexauth.ModelCatalogEntry, error) {
	f.version = version
	if f.list != nil {
		return f.list(ctx, version)
	}
	return f.models, f.modelsErr
}

func TestManagerProjectsStatusAndErrors(t *testing.T) {
	tests := []struct {
		name string
		fake *fakeAuthClient
		want Status
		err  error
	}{
		{name: "missing", fake: &fakeAuthClient{}, want: NotLoggedIn},
		{name: "fresh", fake: &fakeAuthClient{status: codexauth.StatusInfo{LoggedIn: true, AccountID: "SECRET"}}, want: LoggedIn},
		{name: "stale", fake: &fakeAuthClient{status: codexauth.StatusInfo{LoggedIn: true, Stale: true, ConfigPath: "/secret"}}, want: RefreshRequired},
		{name: "read failure", fake: &fakeAuthClient{statusErr: errors.New("TOKEN /secret")}, err: ErrAuth},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := newManager(test.fake).Status(context.Background())
			if got != test.want || !errors.Is(err, test.err) {
				t.Fatalf("Status() = %v, %v", got, err)
			}
		})
	}
}

func TestManagerHTTPClientCategories(t *testing.T) {
	want := &http.Client{}
	got, err := newManager(&fakeAuthClient{http: want}).HTTPClient(context.Background())
	if err != nil || got == want || got.Timeout != want.Timeout || got.Transport == nil {
		t.Fatalf("HTTPClient() = %p, %v", got, err)
	}
	_, err = newManager(&fakeAuthClient{httpErr: codexauth.ErrNotLoggedIn}).HTTPClient(context.Background())
	if !errors.Is(err, codexauth.ErrNotLoggedIn) {
		t.Fatalf("not logged in = %v", err)
	}
	_, err = newManager(&fakeAuthClient{httpErr: errors.New("TOKEN /secret")}).HTTPClient(context.Background())
	if err != ErrAuth || strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("generic auth = %v", err)
	}
}

func TestNewOwnsLoggerStoreAndDeviceOutput(t *testing.T) {
	t.Parallel()
	var options codexauth.Options
	var output bytes.Buffer
	_ = newWithFactory(&output, func(got codexauth.Options) authClient {
		options = got
		return &fakeAuthClient{}
	})
	if options.AppName != codexmodel.AppName || options.Logger == nil || options.Endpoint != "" || options.CredentialPath != "" || options.DevicePrompt == nil {
		t.Fatalf("unsafe options: %#v", options)
	}
	options.Logger.Info("TOKEN", "path", "/secret")
	if output.Len() != 0 {
		t.Fatalf("logger reached prompt output: %q", output.String())
	}
	if err := options.DevicePrompt("https://auth.example/device", "ABCD-1234"); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "Open https://auth.example/device and enter code: ABCD-1234\n" {
		t.Fatalf("device output = %q", got)
	}
	for _, bad := range [][2]string{
		{"https://auth.example/\x1b]0;TOKEN\a", "ABCD"},
		{"https://auth.example/device", "ABCD\nTOKEN"},
		{"http://auth.example/device", "ABCD"},
		{"https://auth.example/device\u202eTOKEN", "ABCD"},
		{"https://auth.example/device\u200dTOKEN", "ABCD"},
		{string([]byte("https://auth.example/device\xff")), "ABCD"},
	} {
		before := output.String()
		if err := options.DevicePrompt(bad[0], bad[1]); !errors.Is(err, ErrAuth) {
			t.Fatalf("unsafe prompt accepted: %q %q", bad[0], bad[1])
		}
		if output.String() != before {
			t.Fatal("unsafe prompt emitted output")
		}
	}
	if err := options.DevicePrompt("https://auth.example/device", strings.Repeat("A", 129)); !errors.Is(err, ErrAuth) {
		t.Fatalf("oversized device code accepted: %v", err)
	}
}

func TestLoginCancellationAndRedaction(t *testing.T) {
	if err := newManager(&fakeAuthClient{}).LoginDevice(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := newManager(&fakeAuthClient{loginErr: context.Canceled}).LoginDevice(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	err := newManager(&fakeAuthClient{loginErr: errors.New("TOKEN /secret\x1b[31m")}).LoginDevice(context.Background())
	if err != ErrAuth || strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("failure = %v", err)
	}
}

func TestManagerCatalogNormalizesAndProjectsFixedErrors(t *testing.T) {
	remote := codexauth.ModelCatalogEntry{
		Slug: "o4-live", DisplayName: "O4 Live", DefaultReasoningEffort: catalogValue("medium"),
		SupportedReasoningEfforts: []codexauth.ReasoningEffortOption{{Effort: "medium", Description: "Balanced"}},
	}
	fake := &fakeAuthClient{models: []codexauth.ModelCatalogEntry{remote}}
	models, err := newManager(fake).ListModels(context.Background())
	if err != nil || fake.version != CatalogCompatibilityVersion || len(models) != 1 || models[0].ModelID != "o4-live" {
		t.Fatalf("models=%#v version=%q err=%v", models, fake.version, err)
	}
	for _, test := range []struct {
		err  error
		want error
	}{
		{errors.New("TOKEN /secret"), ErrCatalogUnavailable},
		{fmt.Errorf("wrapped: %w", context.Canceled), context.Canceled},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), context.DeadlineExceeded},
	} {
		_, err := newManager(&fakeAuthClient{modelsErr: test.err}).ListModels(context.Background())
		if !errors.Is(err, test.want) || strings.Contains(err.Error(), "TOKEN") || strings.Contains(err.Error(), "/secret") {
			t.Fatalf("catalog error=%v want=%v", err, test.want)
		}
	}
	if _, err := (*Manager)(nil).ListModels(context.Background()); !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("nil manager error=%v", err)
	}
}

func TestManagerSerializesCatalogAndProviderThroughHeaderReceipt(t *testing.T) {
	catalogEntered := make(chan struct{})
	releaseCatalog := make(chan struct{})
	providerEntered := make(chan struct{}, 1)
	catalogCalls := 0
	fake := &fakeAuthClient{
		list: func(context.Context, string) ([]codexauth.ModelCatalogEntry, error) {
			catalogCalls++
			if catalogCalls == 1 {
				close(catalogEntered)
				<-releaseCatalog
			}
			return nil, nil
		},
		http: &http.Client{Transport: managerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			providerEntered <- struct{}{}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
		})},
	}
	manager := newManager(fake)
	client, err := manager.HTTPClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	catalogDone := make(chan error, 1)
	go func() { _, listErr := manager.ListModels(context.Background()); catalogDone <- listErr }()
	<-catalogEntered
	providerCtx, cancelProvider := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(providerCtx, http.MethodGet, "https://example.invalid", nil)
	providerDone := make(chan error, 1)
	go func() { _, requestErr := client.Do(request); providerDone <- requestErr }()
	select {
	case <-providerEntered:
		t.Fatal("provider overlapped catalog")
	case <-time.After(20 * time.Millisecond):
	}
	cancelProvider()
	if err := <-providerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting provider error=%v", err)
	}
	close(releaseCatalog)
	if err := <-catalogDone; err != nil {
		t.Fatal(err)
	}

	request, _ = http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-providerEntered:
	case <-time.After(time.Second):
		t.Fatal("provider never entered after catalog release")
	}
	if _, err := manager.ListModels(context.Background()); err != nil {
		t.Fatalf("catalog gate held for provider body: %v", err)
	}
}

func catalogValue(value string) *string { return &value }
