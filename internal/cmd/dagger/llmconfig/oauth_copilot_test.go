package llmconfig

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/internal/version"
	"github.com/stretchr/testify/require"
)

type fakeCopilotServer struct {
	mu           sync.Mutex
	githubTokens []string
	polls        int
	pollErrors   []string
	status       int
	sameToken    bool
	expiresAt    int64
	api          string
	url          string
}

func newFakeCopilotServer(t *testing.T) *fakeCopilotServer {
	t.Helper()
	s := &fakeCopilotServer{expiresAt: time.Now().Add(30 * time.Minute).Unix()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		for name, value := range map[string]string{
			"Accept": "application/json", "Copilot-Integration-Id": "vscode-chat",
			"User-Agent": "dagger/" + version.Version(), "Editor-Version": "dagger/" + version.Version(),
		} {
			if got := r.Header.Get(name); got != value {
				t.Errorf("%s = %q, want %q", name, got, value)
			}
		}
		switch r.URL.Path {
		case "/device", "/oauth":
			if r.Method != http.MethodPost {
				t.Errorf("device flow method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("client_id") != "Iv1.b507a08c87ecfe98" {
				t.Errorf("wrong Copilot client ID: %q", r.Form.Get("client_id"))
			}
			if r.URL.Path == "/device" {
				fmt.Fprint(w, `{"device_code":"device-secret","user_code":"ABCD-1234","verification_uri":"https://github.example/login/device","expires_in":900,"interval":1}`)
				return
			}
			if r.Form.Get("device_code") != "device-secret" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("invalid poll form: %v", r.Form)
			}
			index := s.polls
			s.polls++
			if index < len(s.pollErrors) {
				fmt.Fprintf(w, `{"error":%q}`, s.pollErrors[index])
				return
			}
			fmt.Fprint(w, `{"access_token":"github-device-token"}`)
		case "/exchange":
			if r.Method != http.MethodGet {
				t.Errorf("exchange method = %s", r.Method)
			}
			s.githubTokens = append(s.githubTokens, r.Header.Get("Authorization"))
			if s.status != 0 {
				w.WriteHeader(s.status)
				fmt.Fprint(w, `{"error_description":"credential-leak-marker"}`)
				return
			}
			token := fmt.Sprintf("copilot-%d", len(s.githubTokens))
			if s.sameToken {
				token = "rejected-token"
			}
			fmt.Fprintf(w, `{"token":%q,"expires_at":%d,"endpoints":{"api":%q}}`, token, s.expiresAt, s.api)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	origDevice, origOAuth, origToken := copilotDeviceCodeURL, copilotAccessTokenURL, copilotTokenURL
	t.Cleanup(func() {
		copilotDeviceCodeURL, copilotAccessTokenURL, copilotTokenURL = origDevice, origOAuth, origToken
	})
	copilotDeviceCodeURL, copilotAccessTokenURL, copilotTokenURL = srv.URL+"/device", srv.URL+"/oauth", srv.URL+"/exchange"
	return s
}

func (s *fakeCopilotServer) tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.githubTokens...)
}

// No real polling delay in tests; record the requested intervals so slow_down
// is still verified. All network calls go to httptest, never GitHub/Copilot.
func instantCopilotPolling(t *testing.T) *[]time.Duration {
	t.Helper()
	orig := copilotPollWait
	var intervals []time.Duration
	copilotPollWait = func(ctx context.Context, d time.Duration) error {
		intervals = append(intervals, d)
		return ctx.Err()
	}
	t.Cleanup(func() { copilotPollWait = orig })
	return &intervals
}

type copilotSetupPrompt struct {
	calls   int
	printed string
	opened  string
}

func (p *copilotSetupPrompt) HandleForm(_ context.Context, form *huh.Form) error {
	choice := "github-copilot"
	if p.calls > 0 {
		choice = "gpt-4.1"
	}
	p.calls++
	// Select a real menu option through huh's accessor, avoiding a terminal.
	field := form.GetFocusedField().(*huh.Select[string])
	field.Options(huh.NewOption(choice, choice).Selected(true))
	return nil
}
func (p *copilotSetupPrompt) PrintAbove(s string)        { p.printed += s }
func (p *copilotSetupPrompt) OpenBrowser(s string) error { p.opened = s; return nil }

func TestCopilotDeviceSetup(t *testing.T) {
	useTempConfig(t, nil)
	s := newFakeCopilotServer(t)
	s.pollErrors = []string{"authorization_pending", "slow_down"}
	s.api = "https://enterprise.example"
	intervals := instantCopilotPolling(t)
	ph := &copilotSetupPrompt{}
	configured, err := InteractiveSetup(t.Context(), ph)
	require.NoError(t, err)
	require.True(t, configured)
	require.Contains(t, ph.printed, "ABCD-1234")
	require.Contains(t, ph.printed, "https://github.example/login/device")
	require.Equal(t, "https://github.example/login/device", ph.opened)
	require.Equal(t, []time.Duration{time.Second, time.Second, 6 * time.Second}, *intervals)
	require.Equal(t, []string{"token github-device-token"}, s.tokens())
	cfg, err := Load()
	require.NoError(t, err)
	p := cfg.LLM.Providers["github-copilot"]
	require.True(t, p.Enabled)
	require.Equal(t, "oauth", p.AuthType)
	require.Equal(t, "github-device-token", p.RefreshToken)
	require.Equal(t, "copilot-1", p.AuthToken)
	require.Equal(t, s.expiresAt*1000, p.TokenExpiresAt)
	require.Equal(t, "https://enterprise.example", p.BaseURL)
	require.Equal(t, "gpt-4.1", p.Model)
	require.Equal(t, "github-copilot", cfg.LLM.DefaultProvider)
	require.Equal(t, "gpt-4.1", cfg.LLM.DefaultModel)
	wire, _, err := assemble(cfg, mapEnv(nil), nil)
	require.NoError(t, err)
	require.Equal(t, "github-copilot", wire.DefaultProvider)
	require.Equal(t, "gpt-4.1", wire.DefaultModel)
	require.Equal(t, "llmconfig://github-copilot/auth_token", wire.Providers["github-copilot"].AuthToken)
	require.False(t, IsTokenExpired(&p), "a 30-minute token should not refresh on every resolution")
}

func TestCopilotDeviceSetupDeniedOrExpired(t *testing.T) {
	for _, tc := range []struct{ code, message string }{{"access_denied", "denied"}, {"expired_token", "expired"}, {"unexpected-credential-leak-marker", "authorization failed"}} {
		t.Run(tc.code, func(t *testing.T) {
			useTempConfig(t, nil)
			s := newFakeCopilotServer(t)
			s.pollErrors = []string{tc.code}
			instantCopilotPolling(t)
			configured, err := InteractiveSetup(t.Context(), &copilotSetupPrompt{})
			require.False(t, configured)
			require.ErrorContains(t, err, tc.message)
			if tc.code == "expired_token" {
				require.ErrorContains(t, err, "run /setup in dagger agent again")
			}
			require.NotContains(t, err.Error(), "credential-leak-marker")
			require.False(t, ConfigExists())
			require.Empty(t, s.tokens())
		})
	}
}

func TestCopilotDevicePollDeadlineAndCancellation(t *testing.T) {
	// Already-expired device codes stop before another HTTP request.
	_, err := pollCopilotDeviceToken(t.Context(), copilotDeviceCode{ExpiresIn: 0, Interval: 1})
	require.ErrorContains(t, err, "expired")
	require.ErrorContains(t, err, "run /setup in dagger agent again")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = pollCopilotDeviceToken(ctx, copilotDeviceCode{ExpiresIn: 900, Interval: 1})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCopilotExchangeDefaultEndpointAndInvalidExpiry(t *testing.T) {
	s := newFakeCopilotServer(t)
	p, err := RefreshCopilotOAuthToken(t.Context(), &Provider{RefreshToken: "github", BaseURL: "https://old.example", Enabled: true})
	require.NoError(t, err)
	require.Equal(t, copilotAPIBaseURL, p.BaseURL, "missing endpoints.api must reset a saved endpoint to the default")
	for _, expiry := range []int64{0, time.Now().Add(-time.Minute).Unix()} {
		s.mu.Lock()
		s.expiresAt = expiry
		s.mu.Unlock()
		p, err = RefreshCopilotOAuthToken(t.Context(), &Provider{RefreshToken: "github", Enabled: true})
		require.ErrorContains(t, err, "invalid expiry")
		require.Nil(t, p, "Copilot tokens always require a real expiry")
	}
}

type copilotPlainPrompt struct{ instructions string }

func (p *copilotPlainPrompt) HandleForm(_ context.Context, form *huh.Form) error {
	if note, ok := form.GetFocusedField().(*huh.Note); ok {
		p.instructions = note.View()
	}
	return nil
}

func TestCopilotSetupWithoutBrowserOrAbovePrinter(t *testing.T) {
	useTempConfig(t, nil)
	newFakeCopilotServer(t)
	instantCopilotPolling(t)
	ph := &copilotPlainPrompt{}
	key, p, model, err := setupCopilotOAuth(t.Context(), ph)
	require.NoError(t, err)
	require.Equal(t, "github-copilot", key)
	require.NotEmpty(t, model)
	require.NotEmpty(t, p.AuthToken)
	require.Contains(t, ph.instructions, "ABCD-1234")
	require.Contains(t, ph.instructions, "https://github.example/login/device")
}
