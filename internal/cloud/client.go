package cloud

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/shurcooL/graphql"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var ErrNoOrg = errors.New("no org associated with this Engine")

type Client struct {
	u           *url.URL
	g           *graphql.Client
	h           *http.Client
	engineToken string
}

func NewClient(ctx context.Context) (*Client, error) {
	api := "https://api.dagger.cloud"
	if cloudURL := os.Getenv("DAGGER_CLOUD_URL"); cloudURL != "" {
		api = cloudURL
	}

	u, err := url.Parse(api)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{}

	// Always prefer oauth if available. If not and a DAGGER_CLOUD_TOKEN
	// is set then use Basic auth
	tokenSource, err := auth.TokenSource(ctx)
	if err != nil {
		if cloudToken := os.Getenv("DAGGER_CLOUD_TOKEN"); cloudToken != "" {
			httpClient.Transport, err = auth.DaggerCloudTransport(ctx, cloudToken)
			if err != nil {
				return nil, err
			}

			return &Client{
				u:           u,
				h:           httpClient,
				engineToken: cloudToken,
			}, nil
		}

		return nil, err
	}
	httpClient = oauth2.NewClient(ctx, tokenSource)

	return &Client{
		u: u,
		g: graphql.NewClient(u.JoinPath("/query").String(), httpClient),
		h: httpClient,
	}, nil
}

type UserResponse struct {
	ID   string     `json:"id"`
	Orgs []auth.Org `json:"orgs"`
}

func (c *Client) User(ctx context.Context) (*UserResponse, error) {
	if c.g == nil {
		return nil, errors.New("no user logged in. Using Engine token authentication")
	}
	var q struct {
		User UserResponse `graphql:"user"`
	}
	err := c.g.Query(ctx, &q, nil)
	if err != nil {
		return nil, err
	}

	return &q.User, nil
}

type SerializableCertificate struct {
	CertificateChain [][]byte `json:"certificate_chain"` // DER-encoded certs
	PrivateKey       []byte   `json:"private_key"`       // PKCS#8 encoded private key
	OCSPStaple       []byte   `json:"ocsp_staple,omitempty"`
	SCTs             [][]byte `json:"scts,omitempty"`
}

type EngineRequest struct {
	Module               string   `json:"module,omitempty"`
	Function             string   ` json:"function,omitempty"`
	ExecCmd              []string `json:"exec_cmd,omitempty"`
	ClientID             string   `json:"client_id,omitempty"`
	MinimumEngineVersion string   `json:"minimum_engine_version,omitempty"`
}

type EngineSpec struct {
	EngineRequest

	Image          string                   `json:"image,omitempty"`
	Location       string                   `json:"location,omitempty"`
	OrgID          string                   `json:"org_id,omitempty"`
	UserID         string                   `json:"user_id,omitempty"`
	URL            string                   `json:"url,omitempty"`
	CertSerialized *SerializableCertificate `json:"cert,omitempty"`
}

func (es *EngineSpec) TLSCertificate() (*tls.Certificate, error) {
	if es.CertSerialized == nil {
		return nil, errors.New("serializable certificate is nil")
	}

	// Parse the PKCS#8 DER-encoded private key
	privateKey, err := x509.ParsePKCS8PrivateKey(es.CertSerialized.PrivateKey)
	if err != nil {
		// You might want to try x509.ParsePKCS1PrivateKey or x509.ParseECPrivateKey
		// if PKCS#8 fails, but PKCS#8 should be the most general.
		return nil, fmt.Errorf("failed to parse PKCS#8 private key: %w", err)
	}

	// Re-parse the leaf certificate to populate the Leaf field
	var leaf *x509.Certificate
	if len(es.CertSerialized.CertificateChain) > 0 && len(es.CertSerialized.CertificateChain[0]) > 0 {
		leaf, err = x509.ParseCertificate(es.CertSerialized.CertificateChain[0])
		if err != nil {
			// Non-fatal for tls.Certificate, but good to have
			log.Printf("Warning: could not parse leaf certificate: %v", err)
		}
	}

	return &tls.Certificate{
		Certificate:                 es.CertSerialized.CertificateChain,
		PrivateKey:                  privateKey,
		OCSPStaple:                  es.CertSerialized.OCSPStaple,
		SignedCertificateTimestamps: es.CertSerialized.SCTs,
		Leaf:                        leaf,
	}, nil
}

type ErrResponse struct {
	Message string `json:"message"`
}

func (c *Client) Engine(ctx context.Context, req EngineRequest) (*EngineSpec, error) {
	// Remote Engine version defaults to the CLI version - this guarantees the best compatibility
	tag := engine.Tag
	// Default to `main` when the CLI is a development version
	if tag == "" {
		tag = "main"
	}

	if req.ClientID == "" {
		return nil, errors.New("EngineRequest.ClientID must be set to the value that identifies this client")
	}

	req.MinimumEngineVersion = engine.MinimumEngineVersion
	engineSpec := &EngineSpec{
		Image:         "registry.dagger.io/engine:" + tag,
		EngineRequest: req,
	}
	b, err := json.Marshal(engineSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to json marshal the EngineSpec: %w", err)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.u.JoinPath("/v1/engines").String(), bytes.NewBuffer(b))
	if err != nil {
		return nil, fmt.Errorf("failed to generate a remote Engine request: %w", err)
	}

	if c.engineToken == "" {
		org, err := auth.CurrentOrg()
		if err != nil {
			return nil, ErrNoOrg
		}
		r.Header.Set("X-Dagger-Org", org.ID)
	}

	resp, err := c.h.Do(r)
	if err != nil {
		return nil, fmt.Errorf("failed to request a remote Engine: %w", err)
	}
	defer resp.Body.Close()

	body := json.NewDecoder(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		errResponse := &ErrResponse{}
		err = body.Decode(errResponse)
		if err != nil {
			return nil, fmt.Errorf("response body is not valid JSON: %w", err)
		}
		return nil, fmt.Errorf("failed to provision a remote Engine: %s", errResponse.Message)
	}

	err = body.Decode(engineSpec)
	return engineSpec, err
}
