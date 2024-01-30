package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/pkg/browser"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

const (
	authDomain   = "auth.dagger.cloud"
	callbackPort = 38932
)

var credentialsFile = filepath.Join(xdg.ConfigHome, "dagger", "credentials.json")

var authConfig = &oauth2.Config{
	// https://manage.auth0.com/dashboard/us/dagger-io/applications/brEY7u4SEoFypOgYBdYMs32b4ShRVIEv/settings
	ClientID:    "brEY7u4SEoFypOgYBdYMs32b4ShRVIEv",
	RedirectURL: fmt.Sprintf("http://localhost:%d/callback", callbackPort),
	Scopes:      []string{"openid", "offline_access"},
	Endpoint: oauth2.Endpoint{
		AuthStyle: oauth2.AuthStyleInParams,
		AuthURL:   "https://" + authDomain + "/authorize",
		TokenURL:  "https://" + authDomain + "/oauth/token",
	},
}

// Login logs the user in and stores the credentials for later use.
// Interactive messages are printed to w.
func Login(ctx context.Context) error {
	lg := log.Ctx(ctx)

	lg.Info().Msg("logging in to " + authDomain)

	// oauth2 localhost handler
	requestCh := make(chan *http.Request)

	m := http.NewServeMux()
	// since Login could be called multiple times, only register /callback once
	m.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if oauthError := r.URL.Query().Get("error"); oauthError != "" {
			message := r.URL.Query().Get("error_description")
			fmt.Fprintf(w, `
				<html>
				<head>
				<script>window.close()</script>
				<body>
				%s
				</body>
				</html>
				`, message)
		} else {
			fmt.Fprint(w, `
			<html>
			<head>
			<script>window.location.href="https://dagger.cloud/auth-success"</script>
			<body>
			</body>
			</html>
			`)
		}

		requestCh <- r
	})

	srv := &http.Server{ //nolint: gosec
		Addr:    fmt.Sprintf("localhost:%d", callbackPort),
		Handler: m,
	}

	go func() {
		err := srv.ListenAndServe()
		if err != http.ErrServerClosed {
			lg.Fatal().Err(err).Msg("auth server failed")
		}
	}()

	defer srv.Shutdown(ctx)

	// Generate random state
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Errorf("rand: %w", err)
	}
	state := hex.EncodeToString(b)

	tokenURL := authConfig.AuthCodeURL(state)

	lg.Info().Msgf("opening %s", tokenURL)

	if err := browser.OpenURL(tokenURL); err != nil {
		lg.Warn().Err(err).Msg("could not open browser; please follow the above URL manually")
	}

	var req *http.Request
	select {
	case req = <-requestCh:
	case <-ctx.Done():
		lg.Info().Msg("giving up")
		return nil
	}

	responseState := req.URL.Query().Get("state")
	if state != responseState {
		return fmt.Errorf("corrupted login challenge (%q != %q)", state, responseState)
	}

	if oauthError := req.URL.Query().Get("error"); oauthError != "" {
		description := req.URL.Query().Get("error_description")
		return fmt.Errorf("authentication error: %s (%s)", oauthError, description)
	}

	token, err := authConfig.Exchange(ctx, req.URL.Query().Get("code"))
	if err != nil {
		return err
	}

	return saveCredentials(token)
}

// Logout deletes the client credentials
func Logout() error {
	err := os.Remove(credentialsFile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func TokenSource(ctx context.Context) oauth2.TokenSource {
	return loginTokenSource{ctx}
}

// loginTokenSource is a TokenSource that will re-login if a token is not available or cannot be refreshed.
type loginTokenSource struct {
	ctx context.Context
}

func (src loginTokenSource) Token() (*oauth2.Token, error) {
	token, err := loadCredentials()
	if err != nil {
		if err := Login(src.ctx); err != nil {
			return nil, err
		}

		return src.Token()
	}

	token, err = authConfig.TokenSource(src.ctx, token).Token()
	if err != nil {
		if err := Login(src.ctx); err != nil {
			return nil, err
		}

		return src.Token()
	}

	return token, nil
}

func loadCredentials() (*oauth2.Token, error) {
	data, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, err
	}
	token := &oauth2.Token{}
	if err := json.Unmarshal(data, token); err != nil {
		return nil, err
	}
	return token, nil
}

func saveCredentials(token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(credentialsFile), 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(credentialsFile, data, 0o600); err != nil {
		return err
	}
	return nil
}
