package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagger/dagger/util/parallel"

	"dagger/cli-dev/internal/dagger"
)

type githubClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type githubError struct {
	Method string
	URL    string
	Status int
	Body   string
}

func (err *githubError) Error() string {
	return fmt.Sprintf("%s %s failed: %d %s", err.Method, err.URL, err.Status, err.Body)
}

type githubRelease struct {
	ID        int64  `json:"id"`
	Draft     *bool  `json:"draft"`
	UploadURL string `json:"upload_url"`
}

type githubRepo struct {
	DefaultBranch string `json:"default_branch"`
}

type githubRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type githubContent struct {
	SHA string `json:"sha"`
}

func newGithubClient(
	ctx context.Context,
	githubToken *dagger.Secret,
	githubHost string,
	githubCaCert *dagger.File,
) (*githubClient, error) {
	token := ""
	if githubToken != nil {
		var err error
		token, err = githubToken.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("read GitHub token: %w", err)
		}
	}

	httpClient := http.DefaultClient
	if githubCaCert != nil {
		pem, err := githubCaCert.Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("read GitHub CA certificate: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(pem)) {
			return nil, fmt.Errorf("GitHub CA certificate was not valid PEM")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
		httpClient = &http.Client{Transport: transport}
	}

	return &githubClient{
		baseURL: strings.TrimRight(githubAPIURL(githubHost), "/"),
		token:   token,
		client:  httpClient,
	}, nil
}

func (gh *githubClient) requestJSON(ctx context.Context, method string, path string, payload any, out any) (int, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}

	requestURL := gh.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if gh.token != "" {
		req.Header.Set("Authorization", "Bearer "+gh.token)
	}

	resp, err := gh.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, &githubError{
			Method: method,
			URL:    requestURL,
			Status: resp.StatusCode,
			Body:   string(respBody),
		}
	}
	if out != nil && len(bytes.TrimSpace(respBody)) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (gh *githubClient) releaseByTag(ctx context.Context, owner string, repo string, tag string) (*githubRelease, int, error) {
	var release githubRelease
	status, err := gh.requestJSON(ctx, http.MethodGet, githubPath("repos", owner, repo, "releases", "tags", tag), nil, &release)
	if err != nil {
		return nil, status, err
	}
	return &release, status, nil
}

func (gh *githubClient) upsertRelease(
	ctx context.Context,
	owner string,
	repo string,
	tag string,
	commit string,
	notes string,
) (*githubRelease, error) {
	existing, status, err := gh.releaseByTag(ctx, owner, repo, tag)
	if err != nil && status != http.StatusNotFound {
		return nil, err
	}

	payload := map[string]any{
		"tag_name":         tag,
		"name":             tag,
		"target_commitish": commit,
		"body":             notes,
		"draft":            true,
		"prerelease":       false,
	}
	if status == http.StatusNotFound {
		var release githubRelease
		if _, err := gh.requestJSON(ctx, http.MethodPost, githubPath("repos", owner, repo, "releases"), payload, &release); err != nil {
			return nil, err
		}
		return &release, nil
	}

	if existing.Draft != nil {
		payload["draft"] = *existing.Draft
	}
	var release githubRelease
	if _, err := gh.requestJSON(ctx, http.MethodPatch, githubPath("repos", owner, repo, "releases", fmt.Sprint(existing.ID)), payload, &release); err != nil {
		return nil, err
	}
	if release.UploadURL == "" {
		release.UploadURL = existing.UploadURL
	}
	return &release, nil
}

func (gh *githubClient) publishRelease(ctx context.Context, owner string, repo string, releaseID int64) error {
	_, err := gh.requestJSON(ctx, http.MethodPatch, githubPath("repos", owner, repo, "releases", fmt.Sprint(releaseID)), map[string]any{
		"draft": false,
	}, nil)
	return err
}

func (gh *githubClient) ensureBranch(ctx context.Context, owner string, repo string, branch string) error {
	if _, err := gh.requestJSON(ctx, http.MethodGet, githubPath("repos", owner, repo, "branches", branch), nil, nil); err == nil {
		return nil
	} else if !githubStatus(err, http.StatusNotFound) {
		return err
	}

	base, err := gh.defaultBranch(ctx, owner, repo)
	if err != nil {
		return err
	}

	var ref githubRef
	if _, err := gh.requestJSON(ctx, http.MethodGet, githubPath("repos", owner, repo, "git", "ref", "heads", base), nil, &ref); err != nil {
		return err
	}
	_, err = gh.requestJSON(ctx, http.MethodPost, githubPath("repos", owner, repo, "git", "refs"), map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": ref.Object.SHA,
	}, nil)
	return err
}

func (gh *githubClient) defaultBranch(ctx context.Context, owner string, repo string) (string, error) {
	var response githubRepo
	if _, err := gh.requestJSON(ctx, http.MethodGet, githubPath("repos", owner, repo), nil, &response); err != nil {
		return "", err
	}
	if response.DefaultBranch != "" {
		return response.DefaultBranch, nil
	}
	if repo == "winget-pkgs" {
		return "master", nil
	}
	return "main", nil
}

func (gh *githubClient) writeContent(
	ctx context.Context,
	owner string,
	repo string,
	path string,
	content string,
	branch string,
	message string,
) error {
	contentPath := githubPath("repos", owner, repo, "contents") + "/" + url.PathEscape(path) + "?ref=" + url.QueryEscape(branch)
	var existing githubContent
	status, err := gh.requestJSON(ctx, http.MethodGet, contentPath, nil, &existing)
	if err != nil && status != http.StatusNotFound {
		return err
	}

	payload := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
		"committer": map[string]string{
			"name":  "dagger-bot",
			"email": "noreply@dagger.io",
		},
	}
	if status != http.StatusNotFound && existing.SHA != "" {
		payload["sha"] = existing.SHA
	}

	putPath := githubPath("repos", owner, repo, "contents") + "/" + url.PathEscape(path)
	_, err = gh.requestJSON(ctx, http.MethodPut, putPath, payload, nil)
	return err
}

func (gh *githubClient) mergeUpstream(ctx context.Context, owner string, repo string, branch string) error {
	_, err := gh.requestJSON(ctx, http.MethodPost, githubPath("repos", owner, repo, "merge-upstream"), map[string]string{
		"branch": branch,
	}, nil)
	return err
}

func (gh *githubClient) createPullRequest(
	ctx context.Context,
	owner string,
	repo string,
	title string,
	base string,
	head string,
	body string,
) error {
	_, err := gh.requestJSON(ctx, http.MethodPost, githubPath("repos", owner, repo, "pulls"), map[string]string{
		"title": title,
		"base":  base,
		"head":  head,
		"body":  body,
	}, nil)
	return err
}

func githubStatus(err error, status int) bool {
	var githubErr *githubError
	if !errors.As(err, &githubErr) {
		return false
	}
	return githubErr.Status == status
}

func githubPath(parts ...string) string {
	var path strings.Builder
	for _, part := range parts {
		path.WriteByte('/')
		path.WriteString(url.PathEscape(part))
	}
	return path.String()
}

func (cli *CliDev) publishRootGitHubRelease(
	ctx context.Context,
	dist *dagger.Directory,
	tag string,
	commit string,
	notes string,
	githubOrgName string,
	githubToken *dagger.Secret,
	githubHost string,
	githubCaCert *dagger.File,
) error {
	gh, err := newGithubClient(ctx, githubToken, githubHost, githubCaCert)
	if err != nil {
		return err
	}

	release, err := gh.upsertRelease(ctx, githubOrgName, "dagger", tag, commit, notes)
	if err != nil {
		return err
	}
	if release.ID == 0 {
		return fmt.Errorf("GitHub release ID is empty")
	}
	uploadURL := strings.SplitN(release.UploadURL, "{", 2)[0]
	if uploadURL == "" {
		return fmt.Errorf("GitHub release upload URL is empty")
	}

	jobs := parallel.New()
	for _, asset := range append(cliReleaseArchiveNames(tag), "checksums.txt") {
		asset := asset
		jobs = jobs.WithJob("upload "+asset, func(ctx context.Context) error {
			return cli.uploadGitHubReleaseAsset(ctx, dist, asset, uploadURL, githubToken, githubCaCert)
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return err
	}

	return gh.publishRelease(ctx, githubOrgName, "dagger", release.ID)
}

func (cli *CliDev) uploadGitHubReleaseAsset(
	ctx context.Context,
	dist *dagger.Directory,
	asset string,
	uploadURL string,
	githubToken *dagger.Secret,
	githubCaCert *dagger.File,
) error {
	uploadURL += "?name=" + url.QueryEscape(asset)
	ctr := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   "3.22",
			Packages: []string{"ca-certificates", "curl"},
		}).
		Container().
		With(withGithubCaCert(githubCaCert)).
		WithMountedDirectory("/dist", dist).
		With(optSecretVariable("GITHUB_TOKEN", githubToken)).
		WithEnvVariable("ASSET", asset).
		WithEnvVariable("UPLOAD_URL", uploadURL).
		WithExec([]string{"sh", "-ec", `set -eu
if [ -n "${GITHUB_TOKEN:-}" ]; then
	curl -fsS -X POST \
		-H "Accept: application/vnd.github+json" \
		-H "X-GitHub-Api-Version: 2022-11-28" \
		-H "Authorization: Bearer $GITHUB_TOKEN" \
		-H "Content-Type: application/octet-stream" \
		--data-binary "@/dist/$ASSET" \
		-o /dev/null \
		"$UPLOAD_URL"
else
	curl -fsS -X POST \
		-H "Accept: application/vnd.github+json" \
		-H "X-GitHub-Api-Version: 2022-11-28" \
		-H "Content-Type: application/octet-stream" \
		--data-binary "@/dist/$ASSET" \
		-o /dev/null \
		"$UPLOAD_URL"
fi`})

	_, err := ctr.Sync(ctx)
	return err
}

func (cli *CliDev) publishPackageManagers(
	ctx context.Context,
	dist *dagger.Directory,
	tag string,
	githubOrgName string,
	githubToken *dagger.Secret,
	githubHost string,
	githubCaCert *dagger.File,
	artefactsFQDN string,
) error {
	checksums, err := cli.releaseChecksumMap(ctx, dist)
	if err != nil {
		return err
	}
	nixArchives, err := cli.releaseNixArchives(ctx, dist, tag, checksums)
	if err != nil {
		return err
	}

	version := strings.TrimPrefix(tag, "v")
	baseURL := "https://" + artefactsFQDN + "/dagger/releases/" + version
	gh, err := newGithubClient(ctx, githubToken, githubHost, githubCaCert)
	if err != nil {
		return err
	}

	homebrew, err := homebrewFormula(tag, version, baseURL, checksums)
	if err != nil {
		return err
	}
	if err := gh.writeContent(ctx, githubOrgName, "homebrew-tap", "dagger.rb", homebrew, "main", "Brew formula update for dagger version "+tag); err != nil {
		return err
	}
	if err := gh.writeContent(ctx, githubOrgName, "nix", "pkgs/dagger/default.nix", nixPackage(version, baseURL, nixArchives), "main", "dagger:  -> "+tag); err != nil {
		return err
	}

	wingetBranch := "dagger-" + version
	if err := gh.ensureBranch(ctx, githubOrgName, "winget-pkgs", wingetBranch); err != nil {
		return err
	}
	if err := gh.mergeUpstream(ctx, githubOrgName, "winget-pkgs", "master"); err != nil {
		return err
	}
	manifests, err := wingetManifests(tag, version, baseURL, checksums)
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		if err := gh.writeContent(ctx, githubOrgName, "winget-pkgs", manifest.Path, manifest.Content, wingetBranch, "New version: Dagger.Cli "+version+": add "+manifest.MessageSuffix); err != nil {
			return err
		}
	}
	return gh.createPullRequest(ctx, "microsoft", "winget-pkgs", "New version: Dagger.Cli "+version, "master", githubOrgName+":winget-pkgs:"+wingetBranch, "Automated with Dagger release tooling.")
}
