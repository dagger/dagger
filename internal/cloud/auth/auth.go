package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
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
func Login(ctx context.Context) error {
	// If the user is already authenticated, skip the login process.
	if _, err := Token(ctx); err == nil {
		return nil
	}

	deviceAuth, err := authConfig.DeviceAuth(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nTo authenticate, visit:\n\t%s\n\n", deviceAuth.VerificationURIComplete)

	token, err := authConfig.DeviceAccessToken(ctx, deviceAuth)
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

func TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	token, err := loadCredentials()
	if err != nil {
		return nil, err
	}

	return authConfig.TokenSource(ctx, token), nil
}

func Token(ctx context.Context) (*oauth2.Token, error) {
	tokenSource, err := TokenSource(ctx)
	if err != nil {
		return nil, err
	}
	return tokenSource.Token()
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

	if err := os.WriteFile(orgFile, data, 0o600); err != nil {
		return err
	}
	return nil
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
