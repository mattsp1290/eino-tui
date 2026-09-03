package subscription

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	codexauth "github.com/mattsp1290/codex-auth-go"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
)

type fakeAuthClient struct {
	loginErr  error
	status    codexauth.StatusInfo
	statusErr error
	http      *http.Client
	httpErr   error
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
	if err != nil || got != want {
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
	original := authClientFactory
	t.Cleanup(func() { authClientFactory = original })
	var options codexauth.Options
	authClientFactory = func(got codexauth.Options) authClient {
		options = got
		return &fakeAuthClient{}
	}
	var output bytes.Buffer
	_ = New(&output)
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
