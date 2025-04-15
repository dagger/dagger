package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/gofrs/flock"
	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	"golang.org/x/oauth2"
)

const (
	authDomain = "https://auth.dagger.cloud"
)

var (
	configRoot      = filepath.Join(xdg.ConfigHome, "dagger")
	credentialsFile = filepath.Join(configRoot, "credentials.json")
	orgFile         = filepath.Join(configRoot, "org")
)

var authConfig = &oauth2.Config{
	// https://manage.auth0.com/dashboard/us/dagger-io/applications/brEY7u4SEoFypOgYBdYMs32b4ShRVIEv/settings
	ClientID: "brEY7u4SEoFypOgYBdYMs32b4ShRVIEv",
	Scopes:   []string{"openid", "offline_access"},
	Endpoint: oauth2.Endpoint{
		AuthStyle:     oauth2.AuthStyleInParams,
		AuthURL:       authDomain + "/authorize",
		TokenURL:      authDomain + "/oauth/token",
		DeviceAuthURL: authDomain + "/oauth/device/code",
	},
}

// Login logs the user in and stores the credentials for later use.
// Interactive messages are printed to w.
func Login(ctx context.Context, out io.Writer) error {
	// If the user is already authenticated, skip the login process.
	if _, err := Token(ctx); err == nil {
		return nil
	}

	deviceAuth, err := authConfig.DeviceAuth(ctx)
	if err != nil {
		return err
	}

	authURL := deviceAuth.VerificationURIComplete

	browserBuf := new(strings.Builder)
	browser.Stdout = browserBuf
	browser.Stderr = browserBuf
	if err := browser.OpenURL(authURL); err != nil {
		fmt.Fprintf(out, "Failed to open browser: %s\n\n%s\n", err, browserBuf.String())
		fmt.Fprintf(out, "Authenticate here: %s\n", authURL)
	} else {
		fmt.Fprintf(out, "Browser opened to: %s\n", authURL)
	}

	fmt.Fprintf(out, "Confirmation code: %s\n\n", termenv.String(deviceAuth.UserCode).Bold())

	token, err := authConfig.DeviceAccessToken(ctx, deviceAuth)
	if err != nil {
		return err
	}

	return saveToken(token)
}

// Logout deletes the client credentials
func Logout() error {
	err := os.Remove(credentialsFile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	token, err := Token(ctx)
	if err != nil {
		return nil, err
	}

	return authConfig.TokenSource(ctx, token), nil
}

func Token(ctx context.Context) (*oauth2.Token, error) {
	data, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, err
	}
	token := &oauth2.Token{}
	if err := json.Unmarshal(data, token); err != nil {
		return nil, err
	}
	// Check if the token is still valid
	if token.Valid() {
		return token, nil
	}

	// Refresh
	token, err = authConfig.TokenSource(ctx, token).Token()
	if err != nil {
		return nil, err
	}
	if err := saveToken(token); err != nil {
		return nil, err
	}
	return token, nil
}

func saveToken(token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(credentialsFile), 0o755); err != nil {
		return err
	}

	return writeFile(credentialsFile, data, 0o600)
}

// writeFile writes data to the named file with locking to prevent race conditions
func writeFile(filename string, data []byte, perm os.FileMode) error {
	fileLock := flock.New(filename + ".lock")

	locked, err := fileLock.TryLockContext(context.Background(), 3*time.Second)
	if err != nil {
		return fmt.Errorf("could not acquire lock on %s: %w", filename, err)
	}
	if !locked {
		return fmt.Errorf("could not acquire lock on %s: timed out", filename)
	}

	defer fileLock.Unlock()

	return os.WriteFile(filename, data, perm)
}

type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func CurrentOrg() (*Org, error) {
	data, err := os.ReadFile(orgFile)
	if err != nil {
		return nil, err
	}
	org := Org{}
	if err := json.Unmarshal(data, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

func SetCurrentOrg(org *Org) error {
	if err := os.MkdirAll(filepath.Dir(orgFile), 0o755); err != nil {
		return err
	}

	data, err := json.Marshal(org)
	if err != nil {
		return err
	}

	return writeFile(orgFile, data, 0o600)
}
