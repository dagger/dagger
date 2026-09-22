package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	sectionStart = "<!-- dagger:sdl-diff:start -->"
	sectionEnd   = "<!-- dagger:sdl-diff:end -->"
	schemaPath   = "docs/docs-graphql/schema.graphqls"
)

var prURLPattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([1-9][0-9]*)/?$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type pullRequest struct {
	Body  string `json:"body"`
	State string `json:"state"`
	Base  struct {
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type githubClient struct {
	token  string
	client *http.Client
}

func (g githubClient) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub %s %s: %s", method, path, resp.Status)
	}
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	return nil
}

// Replace exactly one owned section. Never guess at malformed or duplicated
// markers: that could consume prose written by the PR author.
func replaceSection(body, section string) (string, error) {
	starts, ends := strings.Count(body, sectionStart), strings.Count(body, sectionEnd)
	if starts == 0 && ends == 0 {
		if body == "" {
			return section, nil
		}
		return body + "\n\n" + section, nil
	}
	start, end := strings.Index(body, sectionStart), strings.Index(body, sectionEnd)
	if starts != 1 || ends != 1 || end < start {
		return "", fmt.Errorf("malformed or duplicate SDL diff markers; repair them before updating")
	}
	return body[:start] + section + body[end+len(sectionEnd):], nil
}

func renderSection(diff, base, head string, descriptions bool) string {
	command := "go -C hack/sdl-diff run ."
	if !descriptions {
		command += " -descriptions=false"
	}
	command += " " + base + ":" + schemaPath + " " + head + ":" + schemaPath
	content := "No semantic API changes."
	if diff != "" {
		// Descriptions can themselves contain Markdown fences.
		fence := "```"
		for strings.Contains(diff, fence) {
			fence += "`"
		}
		content = fence + "graphql\n" + strings.TrimRight(diff, "\n") + "\n" + fence
	}
	return sectionStart + "\n## API changes\n\n" +
		"`" + command + "`\n\n" +
		content + "\n" + sectionEnd
}

func (g githubClient) updatePR(ctx context.Context, repo, number string, descriptions, publish bool,
	schemas func(context.Context, string, string, string) (string, string, error),
) (string, error) {
	path := "/repos/" + repo + "/pulls/" + number
	var pr pullRequest
	if err := g.request(ctx, "GET", path, nil, &pr); err != nil {
		return "", err
	}
	if publish && pr.State != "open" {
		return "", fmt.Errorf("refusing to update a closed PR")
	}
	if !commitPattern.MatchString(pr.Base.SHA) || !commitPattern.MatchString(pr.Head.SHA) {
		return "", fmt.Errorf("GitHub returned invalid PR commit IDs")
	}
	var comparison struct {
		MergeBase struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}
	if err := g.request(ctx, "GET", "/repos/"+repo+"/compare/"+pr.Base.SHA+"..."+pr.Head.SHA, nil, &comparison); err != nil {
		return "", err
	}
	base := comparison.MergeBase.SHA
	if !commitPattern.MatchString(base) {
		return "", fmt.Errorf("GitHub returned an invalid merge base")
	}
	old, new, err := schemas(ctx, repo, base, pr.Head.SHA)
	if err != nil {
		return "", err
	}
	oldDoc, err := parseDocument("merge base", old, descriptions)
	if err != nil {
		return "", err
	}
	newDoc, err := parseDocument("PR head", new, descriptions)
	if err != nil {
		return "", err
	}
	diff := semanticDiff(oldDoc, newDoc)
	if strings.Contains(diff, sectionStart) || strings.Contains(diff, sectionEnd) {
		return "", fmt.Errorf("SDL diff contains reserved section markers; nothing published")
	}
	section := renderSection(diff, base, pr.Head.SHA, descriptions)
	body, err := replaceSection(pr.Body, section)
	if err != nil {
		return "", err
	}
	if utf8.RuneCountInString(body) > 65536 {
		return "", fmt.Errorf("updated PR description exceeds GitHub's 65536-character limit; nothing published")
	}
	if !publish {
		return section, nil
	}
	if body == pr.Body {
		return "SDL diff section is already up to date.", nil
	}
	// Re-read immediately before writing to catch edits or pushes while the
	// schemas were fetched. GitHub's PR API has no atomic body compare-and-swap.
	var current pullRequest
	if err := g.request(ctx, "GET", path, nil, &current); err != nil {
		return "", err
	}
	if current != pr {
		return "", fmt.Errorf("PR changed while preparing the diff; retry to avoid overwriting concurrent edits")
	}
	if err := g.request(ctx, "PATCH", path, map[string]string{"body": body}, nil); err != nil {
		return "", err
	}
	return "Updated SDL diff section in https://github.com/" + repo + "/pull/" + number + " at " + pr.Head.SHA + ".", nil
}

// Read only committed snapshots, never run code or hooks from the PR. The
// shallow fetch uses the immutable SHAs selected by the GitHub API, including
// fork PR heads reachable through the base repository's pull-request refs.
func fetchSchemas(ctx context.Context, token, repo, base, head string) (string, string, error) {
	dir, err := os.MkdirTemp("", "sdl-diff-pr-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(dir)
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token)))
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s failed (ensure the PR has a committed %s snapshot): %w", args[0], schemaPath, err)
		}
		return string(out), nil
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", "https://github.com/" + repo + ".git"},
		{"config", "remote.origin.promisor", "true"},
		{"config", "remote.origin.partialclonefilter", "blob:none"},
		{"fetch", "--quiet", "--depth=1", "--filter=blob:none", "origin", base, head},
	} {
		if _, err := git(args...); err != nil {
			return "", "", err
		}
	}
	old, err := git("show", base+":"+schemaPath)
	if err != nil {
		return "", "", err
	}
	new, err := git("show", head+":"+schemaPath)
	return old, new, err
}

func runPR(ctx context.Context, url string, descriptions, publish bool) error {
	match := prURLPattern.FindStringSubmatch(url)
	if match == nil {
		return fmt.Errorf("expected a GitHub PR URL: https://github.com/OWNER/REPO/pull/NUMBER")
	}
	token := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if token == "" {
		return fmt.Errorf("GH_TOKEN is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	g := githubClient{token: token, client: &http.Client{Timeout: 30 * time.Second}}
	result, err := g.updatePR(ctx, match[1], match[2], descriptions, publish,
		func(ctx context.Context, repo, base, head string) (string, string, error) {
			return fetchSchemas(ctx, token, repo, base, head)
		})
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}
