package llmconfig

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
)

// ErrAborted is returned when the user cancels a setup form (e.g. ctrl+c).
var ErrAborted = errors.New("setup cancelled")

// isAbort returns true if the error indicates the user pressed Ctrl+C.
func isAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted) || errors.Is(err, ErrAborted)
}

// checkAbort wraps HandleForm: if the user aborted, return ErrAborted.
func checkAbort(err error) error {
	if err != nil && isAbort(err) {
		return ErrAborted
	}
	return err
}

// PromptHandler is the minimal interface needed for interactive prompts
type PromptHandler interface {
	HandleForm(ctx context.Context, form *huh.Form) error
}

// AbovePrinter is an optional interface that PromptHandlers may implement
// to print text above the TUI into the terminal scrollback buffer. This is
// useful for content that must not be word-wrapped (e.g. clickable URLs).
type AbovePrinter interface {
	PrintAbove(text string)
}

// providerSummary returns a short description of an already-configured provider.
func providerSummary(p Provider) string {
	if p.IsOAuth() {
		label := SubscriptionLabel(p.SubscriptionType)
		if label == "" {
			label = "OAuth"
		}
		return label
	}
	if p.APICompat != "" && p.BaseURL != "" {
		return p.BaseURL
	}
	return RedactKey(p.APIKey)
}

// RedactKey returns a redacted version of an API key for display.
// Secret references (e.g. op://, env://) are shown in full; only
// literal tokens are truncated.
func RedactKey(key string) string {
	if strings.Contains(key, "://") {
		return key
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "…" + key[len(key)-4:]
}

// InteractiveSetup guides user through LLM configuration.
// It is additive: already-configured providers are shown with a summary
// and can be reconfigured, while new providers can be added alongside them.
// Returns (configured bool, error) - configured is true if setup completed.
func InteractiveSetup(ctx context.Context, promptHandler PromptHandler) (bool, error) {
	// Load existing config so we can show what's already set up.
	cfg, err := Load()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = make(map[string]Provider)
	}

	// Build provider menu with status indicators.
	entries := ProviderEntries()
	opts := make([]huh.Option[string], 0, len(entries))
	for _, e := range entries {
		label := e.Label
		if p, ok := cfg.LLM.Providers[e.ConfigKey]; ok && p.Enabled {
			// Only show checkmark if the auth type matches this entry.
			if e.IsOAuth == p.IsOAuth() {
				label += " \033[1;32m" + idtui.IconSuccess + " " + providerSummary(p) + "\033[0m"
			}
		}
		opts = append(opts, huh.NewOption(label, e.Value))
	}

	var providerChoice string
	providerForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title("Configure an LLM provider").
				Description("Select a provider to add or reconfigure. You can run setup again to add more.").
				Options(opts...).
				Value(&providerChoice),
		),
	)

	if err := checkAbort(promptHandler.HandleForm(ctx, providerForm)); err != nil {
		return false, err
	}

	// Each flow returns (configKey, *Provider, selectedModel).
	var (
		configKey     string
		providerCfg   *Provider
		selectedModel string
	)
	switch providerChoice {
	case "anthropic-oauth":
		configKey, providerCfg, selectedModel, err = setupClaudeCodeOAuth(ctx, promptHandler)
	case "openai-codex":
		configKey, providerCfg, selectedModel, err = setupOpenAICodexOAuth(ctx, promptHandler)
	case "local":
		configKey, providerCfg, selectedModel, err = setupLocalProvider(ctx, promptHandler)
	default:
		configKey = providerChoice
		providerCfg, selectedModel, err = setupAPIKeyProvider(ctx, promptHandler, providerChoice)
	}
	if err != nil {
		return false, err
	}

	// Merge into existing config.
	cfg.LLM.Providers[configKey] = *providerCfg

	// Set as default, or ask if another default already exists.
	if err := promptSetDefault(ctx, promptHandler, cfg, configKey, selectedModel); err != nil {
		return false, err
	}

	if err := cfg.Save(); err != nil {
		return false, fmt.Errorf("failed to save config: %w", err)
	}

	return true, nil
}

// setupAPIKeyProvider runs the API-key setup flow for a provider and returns
// the configured Provider and selected model without saving to disk.
func setupAPIKeyProvider(ctx context.Context, ph PromptHandler, provider string) (*Provider, string, error) {
	var keyMethod string
	methodForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title(fmt.Sprintf("How would you like to provide your %s API key?", provider)).
				Description("A secret provider reference is preferred over pasting a literal token.").
				Options(
					huh.NewOption("1Password (op://)", "op"),
					huh.NewOption("HashiCorp Vault (vault://)", "vault"),
					huh.NewOption("Environment variable (env://)", "env"),
					huh.NewOption("Command (cmd://)", "cmd"),
					huh.NewOption("File (file://)", "file"),
					huh.NewOption("AWS Secrets Manager (aws+sm://)", "aws+sm"),
					huh.NewOption("AWS Parameter Store (aws+ps://)", "aws+ps"),
					huh.NewOption("Paste literal token", "literal"),
				).
				Value(&keyMethod),
		),
	)

	if err := checkAbort(ph.HandleForm(ctx, methodForm)); err != nil {
		return nil, "", err
	}

	var apiKey string
	switch keyMethod {
	case "op":
		key, err := promptOp(ctx, ph)
		if err != nil {
			return nil, "", err
		}
		apiKey = key
	case "literal":
		key, err := promptLiteralKey(ctx, ph, provider)
		if err != nil {
			return nil, "", err
		}
		apiKey = key
	default:
		key, err := promptSecretRef(ctx, ph, keyMethod)
		if err != nil {
			return nil, "", err
		}
		apiKey = key
	}

	if apiKey == "" {
		return nil, "", ErrAborted
	}

	providerCfg := &Provider{
		APIKey:  apiKey,
		Enabled: true,
	}

	if provider == "openrouter" {
		providerCfg.BaseURL = "https://openrouter.ai/api/v1"
	}

	defaultModel := DefaultModelForProvider(provider)

	selectedModel, err := promptModelSelection(ctx, ph, provider, defaultModel)
	if err != nil {
		return nil, "", err
	}

	if err := promptThinkingConfig(ctx, ph, provider, selectedModel, providerCfg); err != nil {
		return nil, "", err
	}

	return providerCfg, selectedModel, nil
}

// AutoSetupIfNeeded checks if config exists and prompts user to set up if not
// Returns true if setup was completed, false if skipped or already configured
func AutoSetupIfNeeded(ctx context.Context, promptHandler PromptHandler, interactive bool) (bool, error) {
	if LLMConfigured() {
		return false, nil // Already configured
	}

	// Check if we're in an interactive terminal
	// (In non-interactive mode, just fail with helpful error)
	if !interactive {
		return false, nil
	}

	// Prompt to run setup
	var runSetup bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("No LLM configuration found").
				Description("Would you like to configure it now?").
				Value(&runSetup),
		),
	)

	if err := checkAbort(promptHandler.HandleForm(ctx, form)); err != nil {
		return false, err
	}

	if !runSetup {
		return false, nil
	}

	return InteractiveSetup(ctx, promptHandler)
}

// promptOp walks the user through selecting a 1Password secret via the op CLI.
func promptOp(ctx context.Context, ph PromptHandler) (string, error) {
	// List vaults
	vaults := opListVaults()
	if len(vaults) == 0 {
		// Fall back to manual entry
		var ref string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Enter 1Password secret reference").
					Description("Could not list vaults. Is the op CLI installed and signed in?\nEnter a reference manually, e.g. op://vault/item/field").
					Placeholder("op://vault/item/field").
					Value(&ref).
					Validate(validateNonEmpty("secret reference")),
			),
		)
		if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
			return "", err
		}
		if ref == "" {
			return "", ErrAborted
		}
		return ref, nil
	}

	// Pick vault
	var vault string
	vaultOpts := make([]huh.Option[string], 0, len(vaults))
	for _, v := range vaults {
		vaultOpts = append(vaultOpts, huh.NewOption(v, v))
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title("Choose a 1Password vault").
				Options(vaultOpts...).
				Value(&vault),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", err
	}
	if vault == "" {
		return "", ErrAborted
	}

	// List items in vault
	items := opListItems(vault)
	if len(items) == 0 {
		var ref string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Enter item path").
					Description(fmt.Sprintf("No items found in vault %q. Enter the item/field path manually.", vault)).
					Placeholder("item/field").
					Value(&ref).
					Validate(validateNonEmpty("item path")),
			),
		)
		if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
			return "", err
		}
		if ref == "" {
			return "", ErrAborted
		}
		return "op://" + vault + "/" + ref, nil
	}

	// Pick item
	var item string
	itemOpts := make([]huh.Option[string], 0, len(items))
	for _, i := range items {
		itemOpts = append(itemOpts, huh.NewOption(i, i))
	}
	form = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title("Choose an item").
				Options(itemOpts...).
				Value(&item),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", err
	}
	if item == "" {
		return "", ErrAborted
	}

	// List fields in item
	fields := opListFields(vault, item)
	if len(fields) == 0 {
		var field string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Enter field name").
					Description(fmt.Sprintf("No fields found for %s/%s. Enter the field name manually.", vault, item)).
					Placeholder("credential").
					Value(&field).
					Validate(validateNonEmpty("field name")),
			),
		)
		if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
			return "", err
		}
		if field == "" {
			return "", ErrAborted
		}
		return "op://" + vault + "/" + item + "/" + field, nil
	}

	// Pick field
	var field string
	fieldOpts := make([]huh.Option[string], 0, len(fields))
	for _, f := range fields {
		fieldOpts = append(fieldOpts, huh.NewOption(f, f))
	}
	form = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title("Choose a field").
				Options(fieldOpts...).
				Value(&field),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", err
	}
	if field == "" {
		return "", ErrAborted
	}

	return "op://" + vault + "/" + item + "/" + field, nil
}

// promptSecretRef prompts for a secret reference path for the given scheme.
func promptSecretRef(ctx context.Context, ph PromptHandler, scheme string) (string, error) {
	var examples string
	switch scheme {
	case "vault":
		examples = "path/to/secret.field"
	case "env":
		examples = "MY_API_KEY"
	case "cmd":
		examples = "pass show api-key"
	case "file":
		examples = "/run/secrets/api-key"
	case "aws+sm":
		examples = "my-secret-name"
	case "aws+ps":
		examples = "/my/parameter/path"
	case "libsecret":
		examples = "collection/label"
	}

	var path string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Enter %s:// path", scheme)).
				Placeholder(examples).
				Value(&path).
				Validate(validateNonEmpty("secret path")),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", err
	}
	if path == "" {
		return "", ErrAborted
	}
	return scheme + "://" + path, nil
}

// promptLiteralKey prompts for a literal API key with password masking.
func promptLiteralKey(ctx context.Context, ph PromptHandler, provider string) (string, error) {
	var signupURL string
	switch provider {
	case "openrouter":
		signupURL = "https://openrouter.ai/keys"
	case "anthropic":
		signupURL = "https://console.anthropic.com/settings/keys"
	case "openai":
		signupURL = "https://platform.openai.com/api-keys"
	case "google":
		signupURL = "https://aistudio.google.com/app/apikey"
	}

	var key string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Enter your %s API key", provider)).
				Description(fmt.Sprintf("Get a key at: %s", signupURL)).
				EchoMode(huh.EchoModePassword).
				Value(&key).
				Validate(validateNonEmpty("API key")),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", err
	}
	if key == "" {
		return "", ErrAborted
	}
	return key, nil
}

func validateNonEmpty(label string) func(string) error {
	return func(s string) error {
		if s == "" {
			return fmt.Errorf("%s cannot be empty", label)
		}
		return nil
	}
}

// setupClaudeCodeOAuth guides the user through the Claude Code OAuth flow
// and returns the config key, provider config, and selected model.
func setupClaudeCodeOAuth(ctx context.Context, ph PromptHandler) (string, *Provider, string, error) {
	authURL, verifier, err := GenerateOAuthURL()
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to generate OAuth URL: %w", err)
	}

	// Print the URL above the TUI if supported, so it's not word-wrapped
	// and can be Ctrl+Clicked. Otherwise include it inline in the form.
	var authCode string
	var description string
	if printer, ok := ph.(AbovePrinter); ok {
		printer.PrintAbove(fmt.Sprintf(
			"Claude Code OAuth — visit this URL to authorize:\n\n%s\n\n",
			authURL,
		))
		description = "After authorizing, paste the code below."
	} else {
		description = fmt.Sprintf(
			"Visit this URL to authorize:\n\n%s\n\nAfter authorizing, paste the code below.",
			authURL,
		)
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Claude Code OAuth").
				Description(description).
				Placeholder("paste authorization code here").
				Value(&authCode).
				Validate(validateNonEmpty("authorization code")),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", nil, "", err
	}
	if authCode == "" {
		return "", nil, "", ErrAborted
	}

	providerCfg, err := ExchangeOAuthCode(authCode, verifier)
	if err != nil {
		return "", nil, "", fmt.Errorf("OAuth token exchange failed: %w", err)
	}

	defaultModel := DefaultModelForProvider("anthropic")
	selectedModel, err := promptModelSelection(ctx, ph, "anthropic", defaultModel)
	if err != nil {
		return "", nil, "", err
	}

	if err := promptThinkingConfig(ctx, ph, "anthropic", selectedModel, providerCfg); err != nil {
		return "", nil, "", err
	}

	return "anthropic", providerCfg, selectedModel, nil
}

// setupOpenAICodexOAuth guides the user through the OpenAI Codex OAuth flow
// and returns the config key, provider config, and selected model.
func setupOpenAICodexOAuth(ctx context.Context, ph PromptHandler) (string, *Provider, string, error) {
	authURL, verifier, state, err := GenerateOpenAIOAuthURL()
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to generate OAuth URL: %w", err)
	}

	// Start local callback server
	callbackServer, serverErr := StartOAuthCallbackServer(1455, state)

	// Print the URL above the TUI if supported
	if printer, ok := ph.(AbovePrinter); ok {
		printer.PrintAbove(fmt.Sprintf(
			"OpenAI Codex OAuth — visit this URL to authorize:\n\n%s\n\n",
			authURL,
		))
	}

	var code string

	if serverErr == nil {
		defer callbackServer.Close()

		codeCh := make(chan string, 1)
		go func() {
			codeCh <- callbackServer.WaitForCode(ctx)
		}()

		description := "A browser window should open. Or paste the code/URL below."
		if _, ok := ph.(AbovePrinter); !ok {
			description = fmt.Sprintf(
				"Visit this URL to authorize:\n\n%s\n\n%s",
				authURL, description,
			)
		}

		var manualCode string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("OpenAI Codex OAuth").
					Description(description).
					Placeholder("waiting for browser callback (or paste code here)").
					Value(&manualCode),
			),
		)

		formDone := make(chan error, 1)
		go func() {
			formDone <- ph.HandleForm(ctx, form)
		}()

		select {
		case browserCode := <-codeCh:
			if browserCode != "" {
				code = browserCode
			}
		case err := <-formDone:
			if err != nil {
				return "", nil, "", err
			}
			if manualCode != "" {
				code = parseOpenAIAuthInput(manualCode)
			}
		}

		if code == "" && manualCode != "" {
			code = parseOpenAIAuthInput(manualCode)
		}
	} else {
		description := "After authorizing, paste the code or redirect URL below."
		if _, ok := ph.(AbovePrinter); !ok {
			description = fmt.Sprintf(
				"Visit this URL to authorize:\n\n%s\n\n%s",
				authURL, description,
			)
		}

		var manualCode string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("OpenAI Codex OAuth").
					Description(description).
					Placeholder("paste authorization code or redirect URL here").
					Value(&manualCode).
					Validate(validateNonEmpty("authorization code")),
			),
		)
		if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
			return "", nil, "", err
		}
		code = parseOpenAIAuthInput(manualCode)
	}

	if code == "" {
		return "", nil, "", fmt.Errorf("no authorization code received")
	}

	providerCfg, err := ExchangeOpenAIOAuthCode(code, verifier)
	if err != nil {
		return "", nil, "", fmt.Errorf("OAuth token exchange failed: %w", err)
	}

	providerCfg.SubscriptionType = "chatgpt"

	defaultModel := DefaultModelForProvider("openai-codex")
	selectedModel, err := promptModelSelection(ctx, ph, "openai-codex", defaultModel)
	if err != nil {
		return "", nil, "", err
	}

	if err := promptThinkingConfig(ctx, ph, "openai-codex", selectedModel, providerCfg); err != nil {
		return "", nil, "", err
	}

	return "openai-codex", providerCfg, selectedModel, nil
}

// promptSetDefault sets the given provider as default, or asks the user
// if a different default is already configured.
func promptSetDefault(ctx context.Context, ph PromptHandler, cfg *Config, provider, model string) error {
	if cfg.LLM.DefaultProvider == "" || cfg.LLM.DefaultProvider == provider {
		cfg.LLM.DefaultProvider = provider
		cfg.LLM.DefaultModel = model
		return nil
	}

	var setDefault bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Set as default provider?").
				Description(fmt.Sprintf("Current default is %q. Use %q instead?",
					cfg.LLM.DefaultProvider, provider)).
				Value(&setDefault),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return err
	}
	if setDefault {
		cfg.LLM.DefaultProvider = provider
		cfg.LLM.DefaultModel = model
	}
	return nil
}

// promptModelSelection presents a model picker for the given provider.
// If the provider has no catalog entries, falls back to a text input.
func promptModelSelection(ctx context.Context, ph PromptHandler, provider, defaultModel string) (string, error) {
	models := ModelsForProvider(provider)
	if len(models) == 0 {
		// No catalog — accept any model string
		model := defaultModel
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Model").
					Description("Enter the model identifier to use.").
					Value(&model).
					Validate(validateNonEmpty("model")),
			),
		)
		if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
			return "", err
		}
		return model, nil
	}

	// Build select options from catalog
	opts := make([]huh.Option[string], 0, len(models)+1)
	for _, m := range models {
		opts = append(opts, huh.NewOption(m.Label, m.ID))
	}
	opts = append(opts, huh.NewOption("Other (enter manually)", "__other__"))

	selected := defaultModel
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title("Choose a model").
				Options(opts...).
				Value(&selected),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", err
	}

	if selected == "__other__" {
		model := ""
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Model").
					Description("Enter the model identifier.").
					Value(&model).
					Validate(validateNonEmpty("model")),
			),
		)
		if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
			return "", err
		}
		return model, nil
	}

	return selected, nil
}

// promptThinkingConfig asks the user whether to enable extended thinking / reasoning.
// The available options are driven by the model's ReasoningLevels from catwalk.
func promptThinkingConfig(ctx context.Context, ph PromptHandler, provider, model string, cfg *Provider) error {
	m, found := ModelByID(provider, model)
	if !found || !m.CanReason {
		return nil
	}

	// Build options from the model's reasoning levels.
	opts := []huh.Option[string]{
		huh.NewOption("Off (default)", ""),
	}
	for _, level := range m.ReasoningLevels {
		label := strings.ToUpper(level[:1]) + level[1:]
		if level == m.DefaultReasoningEffort {
			label += " (model default)"
		}
		opts = append(opts, huh.NewOption(label, level))
	}

	var mode string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(max(len(opts)+2, 6)).
				Title("Reasoning / thinking").
				Description("Controls how much the model reasons before responding.").
				Options(opts...).
				Value(&mode),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return err
	}

	cfg.ThinkingMode = mode
	return nil
}

// setupLocalProvider guides the user through configuring a local / custom LLM endpoint.
// It returns (configKey, providerCfg, selectedModel, err).
func setupLocalProvider(ctx context.Context, ph PromptHandler) (string, *Provider, string, error) {
	var endpoint string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Endpoint URL").
				Description("The base URL of your local LLM server (e.g. http://192.168.2.225:1234).").
				Placeholder("http://localhost:11434").
				Value(&endpoint).
				Validate(func(s string) error {
					s = strings.TrimSpace(s)
					if s == "" {
						return fmt.Errorf("endpoint URL is required")
					}
					u, err := url.Parse(s)
					if err != nil {
						return fmt.Errorf("invalid URL: %w", err)
					}
					if u.Scheme != "http" && u.Scheme != "https" {
						return fmt.Errorf("URL must start with http:// or https://")
					}
					if u.Host == "" {
						return fmt.Errorf("URL must include a host")
					}
					return nil
				}),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, form)); err != nil {
		return "", nil, "", err
	}
	endpoint = strings.TrimSpace(endpoint)
	// Strip trailing slash for consistency
	endpoint = strings.TrimRight(endpoint, "/")

	var compat string
	compatForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(5).
				Title("API compatibility").
				Description("Which API protocol does this server speak?").
				Options(
					huh.NewOption("OpenAI-compatible (Ollama, LM Studio, vLLM, etc.)", "openai"),
					huh.NewOption("Anthropic-compatible", "anthropic"),
				).
				Value(&compat),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, compatForm)); err != nil {
		return "", nil, "", err
	}

	var model string
	modelForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Model name").
				Description("The model identifier to use (e.g. llama3, qwen2.5-coder, etc.).").
				Placeholder("llama3").
				Value(&model).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("model name is required")
					}
					return nil
				}),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, modelForm)); err != nil {
		return "", nil, "", err
	}
	model = strings.TrimSpace(model)

	// Check for optional API key
	var apiKey string
	keyForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("API key (optional)").
				Description("Leave empty if your server doesn't require authentication.").
				Value(&apiKey),
		),
	)
	if err := checkAbort(ph.HandleForm(ctx, keyForm)); err != nil {
		return "", nil, "", err
	}
	apiKey = strings.TrimSpace(apiKey)

	cfg := &Provider{
		BaseURL:   endpoint,
		APICompat: compat,
		Model:     model,
		Enabled:   true,
	}
	if apiKey != "" {
		cfg.APIKey = apiKey
	}

	return "local", cfg, model, nil
}

// parseOpenAIAuthInput extracts an authorization code from user input.
// The input can be a bare code, a URL with ?code=..., or code#state format.
func parseOpenAIAuthInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	// Try parsing as URL
	if u, err := url.Parse(input); err == nil && u.Scheme != "" {
		if code := u.Query().Get("code"); code != "" {
			return code
		}
	}

	// Try code#state format
	if idx := strings.Index(input, "#"); idx >= 0 {
		return input[:idx]
	}

	// Try code=... format
	if strings.Contains(input, "code=") {
		if vals, err := url.ParseQuery(input); err == nil {
			if code := vals.Get("code"); code != "" {
				return code
			}
		}
	}

	// Bare code
	return input
}
