package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/browser"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
	"golang.org/x/term"
)

const (
	credentialsFile = "~/.config/dagger/credentials" // #nosec
	authDomain      = "auth.dagger.cloud"
	callbackPort    = 38932
)

var (
	authConfig = &oauth2.Config{
		// https://manage.auth0.com/dashboard/us/dagger-io/applications/brEY7u4SEoFypOgYBdYMs32b4ShRVIEv/settings
		ClientID:    "brEY7u4SEoFypOgYBdYMs32b4ShRVIEv",
		RedirectURL: fmt.Sprintf("http://localhost:%d/callback", callbackPort),
		Scopes:      []string{"openid"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://" + authDomain + "/authorize",
			TokenURL: "https://" + authDomain + "/oauth/token",
		},
	}
)

// Login logs the user in and stores the credentials for later use.
// Interactive messages are printed to w.
func Login(ctx context.Context) error {
	lg := log.Ctx(ctx)

	lg.Info().Msg("logging into your dagger account")
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		lg.Error().Msg("login is only supported in interactive mode (stdout is not a terminal)")
		lg.Error().Msg("please log in from a terminal")
		return errors.New("authentication failed")
	}

	// oauth2 localhost handler
	requestCh := make(chan *http.Request)
	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		message := "Authentication successful!"

		if oauthError := r.URL.Query().Get("error"); oauthError != "" {
			message = r.URL.Query().Get("error_description")
		}

		w.Write([]byte(fmt.Sprintf(`
			<html>
			<head>
			<script>window.close()</script>
			<body>
			%s
			</body>
			</html>
			`, message)))
		requestCh <- r
	})
	srv := &http.Server{Addr: fmt.Sprintf("localhost:%d", callbackPort)}
	go func() {
		err := srv.ListenAndServe()
		if err != http.ErrServerClosed {
			lg.Fatal().Err(err).Msg("auth server failed")
		}
	}()

	// Generate random state
	b := make([]byte, 32)
	rand.Read(b)
	state := hex.EncodeToString(b)

	tokenURL := authConfig.AuthCodeURL(state)
	lg.Info().Msgf("opening %s", tokenURL)
	if err := browser.OpenURL(tokenURL); err != nil {
		return err
	}

	r := <-requestCh
	srv.Shutdown(ctx)

	responseState := r.URL.Query().Get("state")
	if state != responseState {
		return fmt.Errorf("corrupted login challenge (%q != %q)", state, responseState)
	}

	if oauthError := r.URL.Query().Get("error"); oauthError != "" {
		description := r.URL.Query().Get("error_description")
		return fmt.Errorf("authentication error: %s (%s)", oauthError, description)
	}

	token, err := authConfig.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		return err
	}

	if err := saveCredentials(token); err != nil {
		return err
	}
	return nil
}

// Logout deletes the client credentials
func Logout() error {
	credentialsFilePath, err := homedir.Expand(credentialsFile)
	if err != nil {
		panic(err)
	}

	err = os.Remove(credentialsFilePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func SetAuthHeader(ctx context.Context, req *http.Request) error {
	lg := log.Ctx(ctx)

	// Load the current token.
	token, err := loadCredentials()
	// Silently ignore errors if we can't find the credentials.
	if err != nil {
		return nil
	}

	// Try and refresh the token
	source := authConfig.TokenSource(ctx, token)
	newToken, err := source.Token()
	if err != nil {
		return err
	}

	// If we did refresh the token, store it back
	if newToken.AccessToken != token.AccessToken || newToken.RefreshToken != token.RefreshToken {
		lg.Debug().Msg("refreshed access token")
		if err := saveCredentials(newToken); err != nil {
			return err
		}
	}

	// Finally, set the auth header
	newToken.SetAuthHeader(req)
	return nil
}

func HasCredentials() bool {
	_, err := loadCredentials()
	return err == nil
}

func loadCredentials() (*oauth2.Token, error) {
	f, err := homedir.Expand(credentialsFile)
	if err != nil {
		return nil, err
	}
	data, err := ioutil.ReadFile(f)
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

	f, err := homedir.Expand(credentialsFile)
	if err != nil {
		panic(err)
	}
	if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		return err
	}

	if err := ioutil.WriteFile(f, data, 0600); err != nil {
		return err
	}
	return nil
}
