package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"
)

// configFile models the custom [[targets]] extension in netlify.toml.
type configFile struct {
	Targets []targetConfig `toml:"targets"`
}

// targetConfig models one [[targets]] table.
type targetConfig struct {
	Git    targetGitConfig    `toml:"git"`
	Deploy targetDeployConfig `toml:"deploy"`
}

// targetGitConfig models git.* selectors for one target.
type targetGitConfig struct {
	Branches            []string `toml:"branches"`
	Tags                []string `toml:"tags"`
	LatestReleaseTag    bool     `toml:"latestReleaseTag"`
	LatestPreReleaseTag bool     `toml:"latestPreReleaseTag"`
}

// targetDeployConfig models deploy.* fields for one target.
type targetDeployConfig struct {
	Site  string            `toml:"site"`
	Prod  bool              `toml:"prod"`
	Draft targetDraftConfig `toml:"draft"`
}

// targetDraftConfig models deploy.draft.* fields for one target.
type targetDraftConfig struct {
	Alias string `toml:"alias"`
}

// targetMatch captures selector-level match details for one target.
type targetMatch struct {
	Branch                   bool   `json:"branch"`
	Tag                      bool   `json:"tag"`
	LatestReleaseTag         bool   `json:"latestReleaseTag"`
	LatestPreReleaseTag      bool   `json:"latestPreReleaseTag"`
	Selected                 bool   `json:"selected"`
	ResolvedLatestReleaseTag string `json:"resolvedLatestReleaseTag"`
	ResolvedLatestPreTag     string `json:"resolvedLatestPreReleaseTag"`
}

// resolvedTarget is the normalized and evaluated target representation emitted as JSON.
type resolvedTarget struct {
	Index                  int         `json:"index"`
	GitBranches            []string    `json:"gitBranches"`
	GitTags                []string    `json:"gitTags"`
	GitLatestReleaseTag    bool        `json:"gitLatestReleaseTag"`
	GitLatestPreReleaseTag bool        `json:"gitLatestPreReleaseTag"`
	DeploySite             string      `json:"deploySite"`
	DeployProd             bool        `json:"deployProd"`
	DeployDraftAlias       string      `json:"deployDraftAlias"`
	Match                  targetMatch `json:"match"`
}

// gitContext is the detected git branch/tag state used for target matching.
type gitContext struct {
	Branch string   `json:"branch"`
	Tags   []string `json:"tags"`
}

// outputPayload is the JSON contract consumed by modules/netlify/netlify.dang.
type outputPayload struct {
	Git     gitContext       `json:"git"`
	Targets []resolvedTarget `json:"targets"`
	Matched []resolvedTarget `json:"matched"`
}

// gitRef tracks a single git ref hash plus optional peeled hash for annotated tags.
type gitRef struct {
	Hash   string
	Peeled string
}

// gitMetadata is the git-derived context read from on-disk refs.
type gitMetadata struct {
	Branch   string
	HeadTags []string
	AllTags  []string
}

// main parses flags, runs resolution, and prints machine-readable JSON.
func main() {
	configPath := flag.String("config", "", "path to netlify.toml")
	flag.Parse()

	if strings.TrimSpace(*configPath) == "" {
		exitf("missing required --config path")
	}

	if err := run(*configPath); err != nil {
		exitf("%v", err)
	}
}

// run loads TOML targets, detects git context, evaluates matches, and emits JSON.
func run(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("netlify target config not found: %s", configPath)
	}

	var cfg configFile
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse %s: %w", configPath, err)
	}

	targets, err := parseTargets(cfg.Targets)
	if err != nil {
		return err
	}

	branch := discoverBranchFromEnv()
	headTags := discoverHeadTagsFromEnv()
	allTags := discoverAllTagsFromEnv()

	meta, metaErr := discoverGitMetadata(configPath)
	if branch == "" && metaErr == nil {
		branch = meta.Branch
	}
	if len(headTags) == 0 && metaErr == nil {
		headTags = meta.HeadTags
	}
	if len(allTags) == 0 && metaErr == nil {
		allTags = meta.AllTags
	}

	if len(allTags) == 0 && hasLatestSelector(targets) {
		if metaErr != nil {
			return errors.New("cannot evaluate git.latestReleaseTag/git.latestPreReleaseTag without git tag metadata")
		}
		return errors.New("cannot evaluate git.latestReleaseTag/git.latestPreReleaseTag without any discovered tags")
	}

	headTags = dedupe(headTags)
	allTags = dedupe(allTags)

	evaluatedTargets, matchedTargets := evaluateTargets(targets, branch, headTags, allTags)
	out := outputPayload{
		Git: gitContext{
			Branch: branch,
			Tags:   headTags,
		},
		Targets: evaluatedTargets,
		Matched: matchedTargets,
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	_, _ = os.Stdout.Write(encoded)
	_, _ = os.Stdout.Write([]byte("\n"))
	return nil
}

// parseTargets validates and normalizes raw TOML targets.
func parseTargets(rawTargets []targetConfig) ([]resolvedTarget, error) {
	resolved := make([]resolvedTarget, 0, len(rawTargets))
	for idx, target := range rawTargets {
		branches, err := normalizeSelectorList(target.Git.Branches, "git.branches", idx)
		if err != nil {
			return nil, err
		}
		tags, err := normalizeSelectorList(target.Git.Tags, "git.tags", idx)
		if err != nil {
			return nil, err
		}

		site := strings.TrimSpace(target.Deploy.Site)
		if site == "" {
			return nil, fmt.Errorf("targets[%d].deploy.site must be a non-empty string", idx)
		}

		alias := strings.TrimSpace(target.Deploy.Draft.Alias)
		if target.Deploy.Prod && alias != "" {
			return nil, fmt.Errorf("targets[%d] invalid deploy mode: deploy.prod and deploy.draft.alias are mutually exclusive", idx)
		}

		if len(branches) == 0 &&
			len(tags) == 0 &&
			!target.Git.LatestReleaseTag &&
			!target.Git.LatestPreReleaseTag {
			return nil, fmt.Errorf(
				"targets[%d] must set at least one git selector (git.branches, git.tags, git.latestReleaseTag, git.latestPreReleaseTag)",
				idx,
			)
		}

		resolved = append(resolved, resolvedTarget{
			Index:                  idx,
			GitBranches:            branches,
			GitTags:                tags,
			GitLatestReleaseTag:    target.Git.LatestReleaseTag,
			GitLatestPreReleaseTag: target.Git.LatestPreReleaseTag,
			DeploySite:             site,
			DeployProd:             target.Deploy.Prod,
			DeployDraftAlias:       alias,
		})
	}

	return resolved, nil
}

// normalizeSelectorList trims, validates, and de-duplicates selector patterns.
func normalizeSelectorList(items []string, fieldName string, targetIndex int) ([]string, error) {
	if len(items) == 0 {
		return []string{}, nil
	}

	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			return nil, fmt.Errorf("targets[%d].%s entries must be non-empty strings", targetIndex, fieldName)
		}
		if _, found := seen[trimmed]; found {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}

// hasLatestSelector reports whether any target needs latest* tag evaluation.
func hasLatestSelector(targets []resolvedTarget) bool {
	for _, target := range targets {
		if target.GitLatestReleaseTag || target.GitLatestPreReleaseTag {
			return true
		}
	}
	return false
}

// evaluateTargets evaluates all targets and returns all + selected subsets.
func evaluateTargets(targets []resolvedTarget, currentBranch string, currentTags []string, allTags []string) ([]resolvedTarget, []resolvedTarget) {
	evaluated := make([]resolvedTarget, 0, len(targets))
	matched := make([]resolvedTarget, 0, len(targets))

	for _, target := range targets {
		eval := evaluateTarget(target, currentBranch, currentTags, allTags)
		evaluated = append(evaluated, eval)
		if eval.Match.Selected {
			matched = append(matched, eval)
		}
	}

	return evaluated, matched
}

// evaluateTarget applies OR selector semantics for one target.
func evaluateTarget(target resolvedTarget, currentBranch string, currentTags []string, allTags []string) resolvedTarget {
	branchMatch := false
	if currentBranch != "" && len(target.GitBranches) > 0 {
		branchMatch = matchesAnyPattern(currentBranch, target.GitBranches)
	}

	tagMatch := false
	if len(currentTags) > 0 && len(target.GitTags) > 0 {
		for _, tag := range currentTags {
			if matchesAnyPattern(tag, target.GitTags) {
				tagMatch = true
				break
			}
		}
	}

	candidateTags := allTags
	if len(target.GitTags) > 0 {
		candidateTags = filterTagsByPatterns(allTags, target.GitTags)
	}

	resolvedLatestReleaseTag := latestSemverTag(candidateTags, false)
	resolvedLatestPreTag := latestSemverTag(candidateTags, true)

	latestReleaseMatch := target.GitLatestReleaseTag &&
		resolvedLatestReleaseTag != "" &&
		slices.Contains(currentTags, resolvedLatestReleaseTag)

	latestPreMatch := target.GitLatestPreReleaseTag &&
		resolvedLatestPreTag != "" &&
		slices.Contains(currentTags, resolvedLatestPreTag)

	target.Match = targetMatch{
		Branch:                   branchMatch,
		Tag:                      tagMatch,
		LatestReleaseTag:         latestReleaseMatch,
		LatestPreReleaseTag:      latestPreMatch,
		Selected:                 branchMatch || tagMatch || latestReleaseMatch || latestPreMatch,
		ResolvedLatestReleaseTag: resolvedLatestReleaseTag,
		ResolvedLatestPreTag:     resolvedLatestPreTag,
	}
	return target
}

// matchesAnyPattern returns true when value matches at least one glob pattern.
func matchesAnyPattern(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchesPattern(value, pattern) {
			return true
		}
	}
	return false
}

// matchesPattern evaluates one shell-style glob pattern against one value.
func matchesPattern(value string, pattern string) bool {
	ok, err := path.Match(pattern, value)
	if err != nil {
		return false
	}
	return ok
}

// filterTagsByPatterns returns all tags matching at least one pattern.
func filterTagsByPatterns(tags []string, patterns []string) []string {
	if len(patterns) == 0 {
		return tags
	}
	filtered := make([]string, 0, len(tags))
	for _, tag := range tags {
		if matchesAnyPattern(tag, patterns) {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

// latestSemverTag returns the greatest semver tag in scope, filtered by prerelease mode.
func latestSemverTag(tags []string, prereleaseOnly bool) string {
	bestRaw := ""
	bestCanonical := ""

	for _, tag := range tags {
		canonical, ok := normalizeSemverTag(tag)
		if !ok {
			continue
		}

		isPre := semver.Prerelease(canonical) != ""
		if prereleaseOnly && !isPre {
			continue
		}
		if !prereleaseOnly && isPre {
			continue
		}

		if bestCanonical == "" || semver.Compare(canonical, bestCanonical) > 0 {
			bestRaw = tag
			bestCanonical = canonical
		}
	}

	return bestRaw
}

// normalizeSemverTag canonicalizes a tag for semver comparison.
func normalizeSemverTag(tag string) (string, bool) {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return "", false
	}
	if !strings.HasPrefix(trimmed, "v") {
		trimmed = "v" + trimmed
	}
	if !semver.IsValid(trimmed) {
		return "", false
	}
	return trimmed, true
}

// discoverBranchFromEnv resolves branch from CI environment overrides.
func discoverBranchFromEnv() string {
	if value := strings.TrimSpace(os.Getenv("NETLIFY_TARGET_GIT_BRANCH")); value != "" {
		return value
	}

	if os.Getenv("GITHUB_REF_TYPE") == "branch" {
		if value := strings.TrimSpace(os.Getenv("GITHUB_REF_NAME")); value != "" {
			return value
		}
	}

	if value := strings.TrimSpace(os.Getenv("GITHUB_HEAD_REF")); value != "" {
		return value
	}

	for _, name := range []string{"CI_COMMIT_BRANCH", "BUILDKITE_BRANCH", "CIRCLE_BRANCH"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}

	return ""
}

// discoverHeadTagsFromEnv resolves HEAD tags from CI environment overrides.
func discoverHeadTagsFromEnv() []string {
	if value := strings.TrimSpace(os.Getenv("NETLIFY_TARGET_GIT_TAG")); value != "" {
		return dedupe(splitCSV(value))
	}

	if os.Getenv("GITHUB_REF_TYPE") == "tag" {
		if value := strings.TrimSpace(os.Getenv("GITHUB_REF_NAME")); value != "" {
			return []string{value}
		}
	}

	for _, name := range []string{"CI_COMMIT_TAG", "BUILDKITE_TAG", "CIRCLE_TAG"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return []string{value}
		}
	}

	return []string{}
}

// discoverAllTagsFromEnv resolves global tag candidates from an optional override.
func discoverAllTagsFromEnv() []string {
	if value := strings.TrimSpace(os.Getenv("NETLIFY_TARGET_GIT_ALL_TAGS")); value != "" {
		return dedupe(splitCSV(value))
	}
	return []string{}
}

// discoverGitMetadata reads git refs directly from .git metadata on disk.
func discoverGitMetadata(configPath string) (gitMetadata, error) {
	repoRoot, err := findRepoRoot(configPath)
	if err != nil {
		return gitMetadata{}, err
	}

	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		return gitMetadata{}, err
	}

	refs, err := readAllRefs(gitDir)
	if err != nil {
		return gitMetadata{}, err
	}

	headRef, headHash, err := readHeadRef(gitDir, refs)
	if err != nil {
		return gitMetadata{}, err
	}

	branch := ""
	if strings.HasPrefix(headRef, "refs/heads/") {
		branch = strings.TrimPrefix(headRef, "refs/heads/")
	}

	tagsAtHead := tagsForHash(refs, headHash)
	allTags := allTagNames(refs)

	return gitMetadata{
		Branch:   branch,
		HeadTags: tagsAtHead,
		AllTags:  allTags,
	}, nil
}

// findRepoRoot walks upward from configPath to locate a directory containing .git.
func findRepoRoot(configPath string) (string, error) {
	start := filepath.Dir(configPath)
	current := filepath.Clean(start)

	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("unable to find .git from config path")
		}
		current = parent
	}
}

// resolveGitDir resolves .git as either a directory or gitdir pointer file.
func resolveGitDir(repoRoot string) (string, error) {
	gitPath := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", err
	}

	if info.IsDir() {
		return gitPath, nil
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", errors.New("invalid .git pointer file")
	}
	refPath := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(refPath) {
		refPath = filepath.Clean(filepath.Join(repoRoot, refPath))
	}
	if _, err := os.Stat(refPath); err != nil {
		return "", err
	}
	return refPath, nil
}

// readAllRefs reads loose refs and packed-refs into one map.
func readAllRefs(gitDir string) (map[string]gitRef, error) {
	refs := map[string]gitRef{}
	if err := readLooseRefs(gitDir, refs); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if err := readPackedRefs(filepath.Join(gitDir, "packed-refs"), refs); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return refs, nil
}

// readLooseRefs walks .git/refs and records hash values for each ref file.
func readLooseRefs(gitDir string, refs map[string]gitRef) error {
	base := filepath.Join(gitDir, "refs")
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hash := strings.TrimSpace(string(data))
		if hash == "" {
			return nil
		}

		rel, err := filepath.Rel(gitDir, path)
		if err != nil {
			return err
		}
		refName := filepath.ToSlash(rel)
		entry := refs[refName]
		entry.Hash = hash
		refs[refName] = entry
		return nil
	})
}

// readPackedRefs parses .git/packed-refs and updates ref hashes/peeled hashes.
func readPackedRefs(packedRefsPath string, refs map[string]gitRef) error {
	file, err := os.Open(packedRefsPath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lastRef := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "^") {
			if lastRef == "" {
				continue
			}
			entry := refs[lastRef]
			entry.Peeled = strings.TrimPrefix(line, "^")
			refs[lastRef] = entry
			continue
		}

		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		refName := parts[1]
		entry := refs[refName]
		entry.Hash = parts[0]
		refs[refName] = entry
		lastRef = refName
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// readHeadRef reads HEAD and resolves either symbolic ref and/or detached hash.
func readHeadRef(gitDir string, refs map[string]gitRef) (string, string, error) {
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", "", err
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return "", "", errors.New("empty HEAD")
	}

	if strings.HasPrefix(line, "ref:") {
		refName := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		entry, ok := refs[refName]
		if !ok {
			return refName, "", nil
		}
		if entry.Peeled != "" {
			return refName, entry.Peeled, nil
		}
		return refName, entry.Hash, nil
	}

	return "", line, nil
}

// tagsForHash returns all tags whose target hash resolves to the provided commit hash.
func tagsForHash(refs map[string]gitRef, hash string) []string {
	if hash == "" {
		return []string{}
	}

	tags := make([]string, 0)
	for refName, ref := range refs {
		if !strings.HasPrefix(refName, "refs/tags/") {
			continue
		}
		candidate := ref.Hash
		if ref.Peeled != "" {
			candidate = ref.Peeled
		}
		if candidate == hash || ref.Hash == hash {
			tags = append(tags, strings.TrimPrefix(refName, "refs/tags/"))
		}
	}

	sort.Strings(tags)
	return dedupe(tags)
}

// allTagNames returns every discovered tag name.
func allTagNames(refs map[string]gitRef) []string {
	tags := make([]string, 0)
	for refName := range refs {
		if strings.HasPrefix(refName, "refs/tags/") {
			tags = append(tags, strings.TrimPrefix(refName, "refs/tags/"))
		}
	}
	sort.Strings(tags)
	return dedupe(tags)
}

// splitCSV splits comma-separated values, trimming and dropping empties.
func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// dedupe preserves order while dropping duplicate strings.
func dedupe(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// exitf prints a formatted error and exits non-zero.
func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
