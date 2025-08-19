package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	apiURL = "https://api.dagger.cloud"
)

func init() {
	if u := os.Getenv("DAGGER_CLOUD_URL"); u != "" {
		apiURL = u
	}
}

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

func CurrentOrgName() (string, error) {
	if cloudToken := os.Getenv("DAGGER_CLOUD_TOKEN"); cloudToken != "" {
		token, ok := ParseDaggerToken(cloudToken)
		if ok {
			return token.orgName, nil
		}
	}
	// if the token is not valid or not present we fall back to reading from
	// disk
	org, err := CurrentOrg()
	if err != nil {
		return "", err
	}
	return org.Name, nil
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

var (
	oidcOnce  sync.Once
	oidcLogin *oidcTokenResponse
	oidcErr   error
)

func fetchOIDCAuth(ctx context.Context) (string, error) {
	// getOIDCToken calls both GitHub OIDC as well as Dagger Cloud's own OIDC endpoint
	// It's not a big deal if we call it more than once per session but

	// it makes sense to avoid it were possible.
	oidcOnce.Do(func() {
		oidcLogin, oidcErr = getOIDCToken(ctx)
	})
	if oidcErr != nil {
		return "", fmt.Errorf("failed to get OIDC token: %w", oidcErr)
	}

	if err := SetCurrentOrg(&Org{ID: oidcLogin.OrgID, Name: oidcLogin.OrgName}); err != nil {
		return "", fmt.Errorf("failed to set current org from OIDC token: %w", err)
	}

	return oidcLogin.Token, nil
}

func GetDaggerCloudAuth(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("DAGGER_CLOUD_TOKEN environment variable is not set")
	}
	if token == "oidc" {
		oidc, err := fetchOIDCAuth(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to fetch OIDC auth: %w", err)
		}
		return "Bearer " + oidc, nil
	}

	return "Basic " + base64.StdEncoding.EncodeToString([]byte(token+":")), nil
}

func DaggerCloudTransport(ctx context.Context, token string) (http.RoundTripper, error) {
	header, err := GetDaggerCloudAuth(ctx, token)
	if err != nil {
		return nil, err
	}

	return &authTransport{header: header}, nil
}

type oidcTokenResponse struct {
	Token   string `json:"token"`
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
}

func getOIDCToken(ctx context.Context) (*oidcTokenResponse, error) {
	// support for GitHub's OIDC environment variables
	ghToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	ghURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	if ghToken != "" && ghURL != "" {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, ghURL+"&audience=dagger.cloud", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitHub's OIDC request: %w", err)
		}
		r.Header.Set("Authorization", "Bearer "+ghToken)

		res, err := http.DefaultClient.Do(r)
		if err != nil {
			return nil, fmt.Errorf("failed to request GitHub's OIDC token: %w", err)
		}
		defer res.Body.Close()

		response := map[string]string{}
		if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
			return nil, fmt.Errorf("failed to decode GitHub's OIDC token response: %w", err)
		}

		providerToken := response["value"]

		r, err = http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/v1/oidc?provider=github", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request for fetching OIDC token: %w", err)
		}
		r.Header.Set("Authorization", "Bearer "+providerToken)

		loginRes, err := http.DefaultClient.Do(r)
		if err != nil {
			return nil, fmt.Errorf("failed to request OIDC token: %w", err)
		}
		defer loginRes.Body.Close()

		tokenResponse := &oidcTokenResponse{}
		if err := json.NewDecoder(loginRes.Body).Decode(&tokenResponse); err != nil {
			return nil, fmt.Errorf("failed to decode OIDC token response: %w", err)
		}
		return tokenResponse, nil
	}

	return nil, fmt.Errorf("OIDC authentication is not supported in this context, please use a DAGGER_CLOUD_TOKEN or 'dagger login'")
}

type authTransport struct {
	header string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.Header.Set("Authorization", t.header)

	return http.DefaultTransport.RoundTrip(r2)
}

type daggerToken struct {
	orgName string
	token   string
}

func ParseDaggerToken(s string) (daggerToken, bool) {
	s, ok := strings.CutPrefix(s, "dag_")
	if !ok {
		return daggerToken{}, false
	}

	orgName, token, ok := strings.Cut(s, "_")
	if !ok {
		return daggerToken{}, false
	}

	return daggerToken{
		orgName: orgName,
		token:   token,
	}, true
}
