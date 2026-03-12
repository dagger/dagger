package llmconfig

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
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

// InteractiveSetup guides user through LLM configuration
// Returns (configured bool, error) - configured is true if setup completed
func InteractiveSetup(ctx context.Context, promptHandler PromptHandler) (bool, error) {
	// 1. Check if LLM is already configured
	if LLMConfigured() {
		// Ask if they want to reconfigure
		var reconfigure bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("LLM configuration already exists").
					Description("Do you want to reconfigure?").
					Value(&reconfigure),
			),
		)
		if err := checkAbort(promptHandler.HandleForm(ctx, form)); err != nil {
			return false, err
		}
		if !reconfigure {
			return false, nil // User chose not to reconfigure
		}
	}

	// 2. Present provider choices
	var providerChoice string
	providerForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title("Choose an LLM provider").
				Description("OpenRouter provides unified access to 100+ models with a single API key").
				Options(
					huh.NewOption("OpenRouter (recommended)", "openrouter"),
					huh.NewOption("Anthropic (Claude Code OAuth - use your Pro/Max subscription)", "anthropic-oauth"),
					huh.NewOption("Anthropic (API key)", "anthropic"),
					huh.NewOption("OpenAI (GPT models)", "openai"),
					huh.NewOption("Google (Gemini models)", "google"),
				).
				Value(&providerChoice),
		),
	)

	if err := checkAbort(promptHandler.HandleForm(ctx, providerForm)); err != nil {
		return false, err
	}

	// Handle Claude Code OAuth flow
	if providerChoice == "anthropic-oauth" {
		return setupClaudeCodeOAuth(ctx, promptHandler)
	}

	// 3. Choose how to provide the API key
	var keyMethod string
	methodForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Height(8).
				Filtering(true).
				Title(fmt.Sprintf("How would you like to provide your %s API key?", providerChoice)).
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

	if err := checkAbort(promptHandler.HandleForm(ctx, methodForm)); err != nil {
		return false, err
	}

	var apiKey string
	switch keyMethod {
	case "op":
		key, err := promptOp(ctx, promptHandler)
		if err != nil {
			return false, err
		}
		apiKey = key
	case "literal":
		key, err := promptLiteralKey(ctx, promptHandler, providerChoice)
		if err != nil {
			return false, err
		}
		apiKey = key
	default:
		// For all other providers, prompt for the path portion after scheme://
		key, err := promptSecretRef(ctx, promptHandler, keyMethod)
		if err != nil {
			return false, err
		}
		apiKey = key
	}

	if apiKey == "" {
		return false, nil
	}

	// 4. Build provider config
	providerCfg := Provider{
		APIKey:  apiKey,
		Enabled: true,
	}

	var defaultModel string
	switch providerChoice {
	case "openrouter":
		defaultModel = "anthropic/claude-sonnet-4.5"
		providerCfg.BaseURL = "https://openrouter.ai/api/v1"
	case "anthropic":
		defaultModel = "claude-sonnet-4.5"
	case "openai":
		defaultModel = "gpt-4.1"
	case "google":
		defaultModel = "gemini-2.5-flash"
	}

	// Load existing config or create new one (preserves non-LLM sections)
	cfg, err := Load()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}

	cfg.LLM = LLMConfig{
		DefaultProvider: providerChoice,
		DefaultModel:    defaultModel,
		Providers: map[string]Provider{
			providerChoice: providerCfg,
		},
	}

	if err := cfg.Save(); err != nil {
		return false, fmt.Errorf("failed to save config: %w", err)
	}

	return true, nil
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

// setupClaudeCodeOAuth guides the user through the Claude Code OAuth flow.
// This allows using a Claude Pro/Max subscription instead of an API key.
func setupClaudeCodeOAuth(ctx context.Context, ph PromptHandler) (bool, error) {
	// Generate the OAuth URL
	authURL, verifier, err := GenerateOAuthURL()
	if err != nil {
		return false, fmt.Errorf("failed to generate OAuth URL: %w", err)
	}

	// Print the URL above the TUI if supported, so it's not word-wrapped
	// and can be Ctrl+Clicked. Otherwise include it inline in the form.
	var authCode string
	var description string
	if printer, ok := ph.(AbovePrinter); ok {
		printer.PrintAbove(fmt.Sprintf(
			"Claude Code OAuth — visit this URL to authorize:\n\n%s\n",
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
		return false, err
	}
	if authCode == "" {
		return false, ErrAborted
	}

	// Exchange the code for tokens
	providerCfg, err := ExchangeOAuthCode(authCode, verifier)
	if err != nil {
		return false, fmt.Errorf("OAuth token exchange failed: %w", err)
	}

	// Load existing config or create new one
	cfg, err := Load()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}

	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = make(map[string]Provider)
	}

	cfg.LLM.DefaultProvider = "anthropic"
	cfg.LLM.DefaultModel = "claude-sonnet-4-5"
	cfg.LLM.Providers["anthropic"] = *providerCfg

	if err := cfg.Save(); err != nil {
		return false, fmt.Errorf("failed to save config: %w", err)
	}

	return true, nil
}
