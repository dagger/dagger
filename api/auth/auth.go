package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/browser"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

const (
	credentialsFile = "~/.config/dagger/credentials" // #nosec
	authDomain      = "auth.dagger.cloud"
	callbackPort    = 38932
)

var authConfig = &oauth2.Config{
	// https://manage.auth0.com/dashboard/us/dagger-io/applications/brEY7u4SEoFypOgYBdYMs32b4ShRVIEv/settings
	ClientID:    "brEY7u4SEoFypOgYBdYMs32b4ShRVIEv",
	RedirectURL: fmt.Sprintf("http://localhost:%d/callback", callbackPort),
	Scopes:      []string{"openid", "offline_access"},
	Endpoint: oauth2.Endpoint{
		AuthStyle:     oauth2.AuthStyleInParams,
		AuthURL:       "https://" + authDomain + "/authorize",
		TokenURL:      "https://" + authDomain + "/oauth/token",
		DeviceAuthURL: "https://" + authDomain + "/oauth/device/code",
	},
}

// Login logs the user in and stores the credentials for later use.
// Interactive messages are printed to w.
func Login(ctx context.Context) error {
	lg := log.Ctx(ctx)
	lg.Info().Msg("logging into your dagger account")

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

	srv := &http.Server{Addr: fmt.Sprintf("localhost:%d", callbackPort), Handler: m}
	go func() {
		err := srv.ListenAndServe()
		if err != http.ErrServerClosed {
			lg.Fatal().Err(err).Msg("auth server failed")
		}
	}()

	var token *oauth2.Token

	// Generate random state
	b := make([]byte, 32)
	rand.Read(b)
	state := hex.EncodeToString(b)

	tokenURL := authConfig.AuthCodeURL(state)
	lg.Info().Msgf("attempting to open %s", tokenURL)
	if err := browser.OpenURL(tokenURL); err != nil {
		lg.Error().Msg("could not open browser, attempting device auth flow")
		da, err := authConfig.AuthDevice(ctx)
		if err != nil {
			return err
		}

		lg.Info().Msgf("visit the following link to authorize this CLI instance: %s", da.VerificationURIComplete)

		// use a moderate interval, the `Poll` method below will do a backoff
		// in case the server returns an error to slow down.
		token, err = authConfig.Poll(ctx, da)
		if err != nil {
			return err
		}
	} else {
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

		token, err = authConfig.Exchange(ctx, r.URL.Query().Get("code"))
		if err != nil {
			return err
		}
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
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}

	if err := ioutil.WriteFile(f, data, 0o600); err != nil {
		return err
	}
	return nil
}
