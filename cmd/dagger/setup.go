package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var (
	setupJSONOutput bool
	setupYes        bool
)

var setupCmd = &cobra.Command{
	Use:     "setup",
	Aliases: []string{"migrate"},
	Short:   "Ensure your Dagger workspace is properly setup and operational",
	Long: `Ensure your Dagger workspace is properly setup and operational.

This command is re-entrant. Run it when creating a workspace, after pulling
changes, or after upgrading Dagger.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetup(cmd)
	},
}

func init() {
	setupCmd.Flags().BoolVar(&setupJSONOutput, "json", false, "Output setup status as JSON")
	setupCmd.Flags().BoolVarP(&setupYes, "yes", "y", false, "Automatically run safe setup actions")
}

const (
	setupStatusDone    = "done"
	setupStatusTodo    = "todo"
	setupStatusBlocked = "blocked"
	setupStatusSkipped = "skipped"
	setupStatusFailed  = "failed"
)

type setupReport struct {
	Status    string             `json:"status"`
	Steps     []*setupStepResult `json:"steps"`
	NextSteps []string           `json:"next_steps"`
}

type setupStepResult struct {
	ID             string   `json:"id"`
	Status         string   `json:"status"`
	TLDR           string   `json:"tldr"`
	Reason         string   `json:"reason,omitempty"`
	Command        []string `json:"command,omitempty"`
	CommandString  string   `json:"command_string,omitempty"`
	SafeToRun      bool     `json:"safe_to_run"`
	HumanRequired  bool     `json:"human_required"`
	NextSteps      []string `json:"next_steps,omitempty"`
	Recommendation []string `json:"recommendations,omitempty"`
}

type setupRunner struct {
	cmd    *cobra.Command
	ctx    context.Context
	state  setupState
	report setupReport
}

type setupState struct {
	configPath     string
	configExists   bool
	detectedSDK    string
	githubRemote   bool
	loggedIn       bool
	currentOrgName string
}

type setupAction func(*setupRunner) (*setupStepResult, error)

var setupActions = []setupAction{
	setupModule,
	setupRecommendations,
	setupDevelop,
	setupWorkspaceState,
	setupLogin,
	setupGitHubIntegration,
}

var setupHints = []string{
	"To run workspace checks: `dagger check`",
	"To run workspace generators: `dagger generate`",
	"To inspect available functions: `dagger functions`",
	"To watch workspace activity: `dagger --web check`",
	"To develop your own module: https://docs.dagger.io/extending/",
}

func runSetup(cmd *cobra.Command) error {
	runner := &setupRunner{
		cmd: cmd,
		ctx: cmd.Context(),
		report: setupReport{
			Status: setupStatusDone,
		},
	}

	for _, action := range setupActions {
		res, err := action(runner)
		if err != nil {
			if res == nil {
				res = &setupStepResult{}
			}
			res.Status = setupStatusFailed
			if res.TLDR == "" {
				res.TLDR = "setup step failed"
			}
			res.Reason = err.Error()
			runner.report.Steps = append(runner.report.Steps, res)
			runner.report.Status = setupStatusFailed
			if setupJSONOutput {
				_ = runner.writeJSON()
			}
			return err
		}
		runner.report.Steps = append(runner.report.Steps, res)
	}

	runner.report.NextSteps = setupHints
	runner.report.Status = setupOverallStatus(runner.report.Steps)

	if setupJSONOutput {
		if err := runner.writeJSON(); err != nil {
			return err
		}
	} else {
		runner.writeHuman()
	}

	if runner.report.Status == setupStatusBlocked {
		return idtui.ExitError{OriginalCode: 2}
	}
	return nil
}

func setupModule(r *setupRunner) (*setupStepResult, error) {
	configPath, exists, err := findDaggerConfig(".")
	if err != nil {
		return &setupStepResult{ID: "module"}, err
	}

	r.state.configPath = configPath
	r.state.configExists = exists
	r.state.detectedSDK = detectSetupSDK(".")

	if exists {
		return setupDone("module", "Dagger module config found", fmt.Sprintf("Found %s.", configPath)), nil
	}

	cmd := []string{"dagger", "init"}
	if r.state.detectedSDK != "" {
		cmd = append(cmd, "--sdk="+r.state.detectedSDK)
	}

	runInit := setupYes
	if !runInit && !setupJSONOutput {
		runInit, err = setupConfirm(r.ctx, "Initialize Dagger module?", strings.Join(cmd, " "))
		if err != nil {
			return setupCommandResult("module", setupStatusFailed, "Failed to confirm module initialization", cmd, true, false), err
		}
	}

	if runInit {
		if err := r.runDaggerCommand(cmd[1:]...); err != nil {
			return setupCommandResult("module", setupStatusFailed, "Failed to initialize Dagger module", cmd, true, false), err
		}
		configPath, exists, err = findDaggerConfig(".")
		if err != nil {
			return &setupStepResult{ID: "module"}, err
		}
		r.state.configPath = configPath
		r.state.configExists = exists
		return setupDone("module", "Initialized Dagger module", strings.Join(cmd, " ")), nil
	}

	return setupCommandResult(
		"module",
		setupStatusBlocked,
		"Dagger module is not initialized",
		cmd,
		true,
		false,
	), nil
}

func setupRecommendations(r *setupRunner) (*setupStepResult, error) {
	if !r.state.configExists {
		return setupSkipped("recommendations", "Skipped recommendations until module exists", ""), nil
	}

	recs, err := setupRecommendationsFor(".")
	if err != nil {
		return &setupStepResult{ID: "recommendations"}, err
	}
	if len(recs) == 0 {
		return setupDone("recommendations", "No recommended modules found", ""), nil
	}

	res := setupCommandResult(
		"recommendations",
		setupStatusTodo,
		fmt.Sprintf("Found %d recommended module(s)", len(recs)),
		[]string{"dagger", "install", recs[0].Address},
		true,
		false,
	)
	for _, rec := range recs {
		res.Recommendation = append(res.Recommendation, rec.String())
	}

	processed := false
	for _, rec := range recs {
		install := setupYes
		if !setupYes && !setupJSONOutput {
			var err error
			install, err = setupConfirm(r.ctx, fmt.Sprintf("Install %s?", rec.Name), rec.String())
			if err != nil {
				return res, err
			}
			if hasTTY {
				processed = true
			}
		}
		if !install {
			continue
		}
		if err := r.runDaggerCommand("install", rec.Address); err != nil {
			return setupCommandResult("recommendations", setupStatusFailed, "Failed to install recommended module", []string{"dagger", "install", rec.Address}, true, false), err
		}
		processed = true
	}

	if setupYes || processed {
		res.Status = setupStatusDone
		res.TLDR = "Processed recommended modules"
	}
	return res, nil
}

func setupDevelop(r *setupRunner) (*setupStepResult, error) {
	if !r.state.configExists {
		return setupSkipped("develop", "Skipped code generation until module exists", ""), nil
	}

	needsDevelop, reason, err := moduleNeedsDevelop(r.state.configPath)
	if err != nil {
		return &setupStepResult{ID: "develop"}, err
	}
	if !needsDevelop {
		return setupDone("develop", "No SDK or clients require generation", reason), nil
	}

	cmd := []string{"dagger", "develop"}
	if setupJSONOutput && !setupYes {
		return setupCommandResult("develop", setupStatusTodo, "Generated files may need refresh", cmd, true, false), nil
	}
	if err := r.runDaggerCommand(cmd[1:]...); err != nil {
		return setupCommandResult("develop", setupStatusFailed, "Failed to refresh generated files", cmd, true, false), err
	}
	return setupDone("develop", "Refreshed generated files", strings.Join(cmd, " ")), nil
}

func setupWorkspaceState(r *setupRunner) (*setupStepResult, error) {
	cmd := []string{"dagger", "lock", "update"}
	if setupJSONOutput && !setupYes {
		return setupCommandResult("workspace", setupStatusTodo, "Workspace state can be refreshed", cmd, true, false), nil
	}
	if err := r.runDaggerCommand(cmd[1:]...); err != nil {
		return setupCommandResult("workspace", setupStatusFailed, "Failed to refresh workspace state", cmd, true, false), err
	}
	return setupDone("workspace", "Refreshed workspace state", strings.Join(cmd, " ")), nil
}

func setupLogin(r *setupRunner) (*setupStepResult, error) {
	if _, err := auth.Token(r.ctx); err == nil {
		r.state.loggedIn = true
		if org, orgErr := auth.CurrentOrgName(); orgErr == nil {
			r.state.currentOrgName = org
		}
		return setupDone("auth", "Dagger Cloud login is configured", ""), nil
	}

	cmd := []string{"dagger", "login"}
	if setupJSONOutput || setupYes || !hasTTY {
		return setupCommandResult("auth", setupStatusBlocked, "Dagger Cloud login requires a human", cmd, false, true), nil
	}

	if err := cloudCLI.Login(r.cmd, nil); err != nil {
		return setupCommandResult("auth", setupStatusFailed, "Failed to configure Dagger Cloud login", cmd, false, true), err
	}
	r.state.loggedIn = true
	if org, err := auth.CurrentOrgName(); err == nil {
		r.state.currentOrgName = org
	}
	return setupDone("auth", "Configured Dagger Cloud login", strings.Join(cmd, " ")), nil
}

func setupGitHubIntegration(r *setupRunner) (*setupStepResult, error) {
	r.state.githubRemote = hasGitHubRemote(".")
	if !r.state.githubRemote {
		return setupSkipped("github", "No GitHub remote detected", ""), nil
	}

	if !r.state.loggedIn {
		return setupCommandResult(
			"github",
			setupStatusBlocked,
			"GitHub integration requires Dagger Cloud login first",
			[]string{"dagger", "login"},
			false,
			true,
		), nil
	}

	next := "Open Dagger Cloud Settings > Git Sources and install the GitHub application."
	if r.state.currentOrgName != "" {
		settings := "https://dagger.cloud/" + url.PathEscape(r.state.currentOrgName) + "/settings"
		next = fmt.Sprintf("%s %s", next, settings)
	}
	return &setupStepResult{
		ID:            "github",
		Status:        setupStatusTodo,
		TLDR:          "GitHub integration should be verified manually",
		Reason:        "This CLI build has no API to detect or install the Dagger Cloud GitHub integration.",
		HumanRequired: true,
		NextSteps:     []string{next, "Run `dagger setup` again after connecting GitHub."},
	}, nil
}

func setupOverallStatus(steps []*setupStepResult) string {
	status := setupStatusDone
	for _, step := range steps {
		switch step.Status {
		case setupStatusFailed:
			return setupStatusFailed
		case setupStatusBlocked:
			status = setupStatusBlocked
		case setupStatusTodo:
			if status == setupStatusDone {
				status = setupStatusTodo
			}
		}
	}
	return status
}

func (r *setupRunner) writeJSON() error {
	enc := json.NewEncoder(r.cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(r.report)
}

func (r *setupRunner) writeHuman() {
	out := r.cmd.OutOrStdout()
	for _, step := range r.report.Steps {
		fmt.Fprintf(out, "%s - %s\n", strings.ToUpper(step.Status), step.TLDR)
		if step.Reason != "" {
			fmt.Fprintf(out, "  %s\n", step.Reason)
		}
		if step.CommandString != "" {
			fmt.Fprintf(out, "  $ %s\n", step.CommandString)
		}
		for _, next := range step.NextSteps {
			fmt.Fprintf(out, "  next: %s\n", next)
		}
		for _, rec := range step.Recommendation {
			fmt.Fprintf(out, "  recommendation: %s\n", rec)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps")
	for _, hint := range r.report.NextSteps {
		fmt.Fprintf(out, "- %s\n", hint)
	}
}

func (r *setupRunner) runDaggerCommand(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(r.ctx, exe, args...)
	cmd.Stdin = stdin
	if setupJSONOutput {
		cmd.Stdout = r.cmd.ErrOrStderr()
	} else {
		cmd.Stdout = r.cmd.OutOrStdout()
	}
	cmd.Stderr = r.cmd.ErrOrStderr()
	return cmd.Run()
}

func setupDone(id, tldr, reason string) *setupStepResult {
	return &setupStepResult{
		ID:     id,
		Status: setupStatusDone,
		TLDR:   tldr,
		Reason: reason,
	}
}

func setupSkipped(id, tldr, reason string) *setupStepResult {
	return &setupStepResult{
		ID:     id,
		Status: setupStatusSkipped,
		TLDR:   tldr,
		Reason: reason,
	}
}

func setupCommandResult(id, status, tldr string, cmd []string, safe, human bool) *setupStepResult {
	return &setupStepResult{
		ID:            id,
		Status:        status,
		TLDR:          tldr,
		Command:       cmd,
		CommandString: strings.Join(cmd, " "),
		SafeToRun:     safe,
		HumanRequired: human,
		NextSteps:     []string{"Run `dagger setup` again after this step completes."},
	}
}

func setupConfirm(ctx context.Context, title, description string) (bool, error) {
	if !hasTTY {
		return false, nil
	}

	var confirm bool
	form := idtui.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Description(description).
				Affirmative("Install").
				Negative("Skip").
				Value(&confirm),
		),
	)
	if err := Frontend.HandleForm(ctx, form); err != nil {
		return false, err
	}
	return confirm, nil
}

func findDaggerConfig(start string) (string, bool, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false, err
	}
	for {
		candidate := filepath.Join(dir, "dagger.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true, nil
		} else if !os.IsNotExist(err) {
			return "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

func detectSetupSDK(root string) string {
	switch {
	case exists(root, "go.mod"):
		return "go"
	case exists(root, "package.json"), exists(root, "tsconfig.json"):
		return "typescript"
	case exists(root, "pyproject.toml"), exists(root, "requirements.txt"):
		return "python"
	default:
		return ""
	}
}

func moduleNeedsDevelop(configPath string) (bool, string, error) {
	cfg, err := readDaggerConfig(configPath)
	if err != nil {
		return false, "", err
	}
	if cfg.SDKSource() != "" {
		return true, "Module SDK is configured.", nil
	}
	if len(cfg.Clients) > 0 {
		return true, "Module clients are configured.", nil
	}
	return false, "No SDK or clients configured.", nil
}

type setupDaggerConfig struct {
	SDK          any                      `json:"sdk"`
	Clients      []map[string]any         `json:"clients"`
	Dependencies []*setupConfigDependency `json:"dependencies"`
}

type setupConfigDependency struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

func readDaggerConfig(path string) (*setupDaggerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg setupDaggerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (cfg *setupDaggerConfig) SDKSource() string {
	switch sdk := cfg.SDK.(type) {
	case string:
		return sdk
	case map[string]any:
		if src, ok := sdk["source"].(string); ok {
			return src
		}
	}
	return ""
}

type setupRecommendation struct {
	Name    string
	Address string
	Reason  string
}

func (rec setupRecommendation) String() string {
	return fmt.Sprintf("%s matches %s; run `dagger install %s`", rec.Name, rec.Reason, rec.Address)
}

var setupRecommendationRules = []struct {
	name    string
	address string
	reason  string
	match   func(string) bool
}{
	{
		name:    "gha",
		address: "github.com/dagger/dagger/modules/gha",
		reason:  ".github/workflows/",
		match: func(root string) bool {
			return isDir(root, ".github", "workflows")
		},
	},
	{
		name:    "ruff",
		address: "github.com/dagger/dagger/modules/ruff",
		reason:  "pyproject.toml or requirements.txt",
		match: func(root string) bool {
			return exists(root, "pyproject.toml") || exists(root, "requirements.txt")
		},
	},
	{
		name:    "markdownlint",
		address: "github.com/dagger/dagger/modules/markdownlint",
		reason:  ".markdownlint config",
		match: func(root string) bool {
			return exists(root, ".markdownlint.json") || exists(root, ".markdownlint.yaml") || exists(root, ".markdownlint.yml")
		},
	},
}

func setupRecommendationsFor(root string) ([]setupRecommendation, error) {
	configPath, existsConfig, err := findDaggerConfig(root)
	if err != nil {
		return nil, err
	}
	var installed map[string]bool
	if existsConfig {
		installed, err = installedDependencySources(configPath)
		if err != nil {
			return nil, err
		}
	} else {
		installed = map[string]bool{}
	}

	var recs []setupRecommendation
	for _, rule := range setupRecommendationRules {
		if !rule.match(root) || installed[rule.address] {
			continue
		}
		recs = append(recs, setupRecommendation{
			Name:    rule.name,
			Address: rule.address,
			Reason:  rule.reason,
		})
	}
	return recs, nil
}

func installedDependencySources(configPath string) (map[string]bool, error) {
	cfg, err := readDaggerConfig(configPath)
	if err != nil {
		return nil, err
	}
	installed := map[string]bool{}
	for _, dep := range cfg.Dependencies {
		if dep == nil {
			continue
		}
		installed[dep.Source] = true
		if strings.HasPrefix(dep.Source, "github.com/dagger/dagger/modules/") {
			installed[dep.Source] = true
		}
	}
	return installed, nil
}

func hasGitHubRemote(root string) bool {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "github.com")
}

func exists(root string, paths ...string) bool {
	_, err := os.Stat(filepath.Join(append([]string{root}, paths...)...))
	return err == nil
}

func isDir(root string, paths ...string) bool {
	info, err := os.Stat(filepath.Join(append([]string{root}, paths...)...))
	return err == nil && info.IsDir()
}
