package llmconfig

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
)

// PromptHandler is the minimal interface needed for interactive prompts
type PromptHandler interface {
	HandleForm(ctx context.Context, form *huh.Form) error
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
		if err := promptHandler.HandleForm(ctx, form); err != nil {
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
				Title("Choose an LLM provider").
				Description("OpenRouter provides unified access to 100+ models with a single API key").
				Options(
					huh.NewOption("OpenRouter (recommended)", "openrouter"),
					huh.NewOption("Anthropic (Claude models)", "anthropic"),
					huh.NewOption("OpenAI (GPT models)", "openai"),
					huh.NewOption("Google (Gemini models)", "google"),
				).
				Value(&providerChoice),
		),
	)

	if err := promptHandler.HandleForm(ctx, providerForm); err != nil {
		return false, err
	}

	// 3. Get API key for chosen provider
	var apiKey string
	var signupURL string

	switch providerChoice {
	case "openrouter":
		signupURL = "https://openrouter.ai/keys"
	case "anthropic":
		signupURL = "https://console.anthropic.com/settings/keys"
	case "openai":
		signupURL = "https://platform.openai.com/api-keys"
	case "google":
		signupURL = "https://aistudio.google.com/app/apikey"
	}

	keyForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Enter your %s API key", providerChoice)).
				Description(fmt.Sprintf("Get your key at: %s", signupURL)).
				EchoMode(huh.EchoModePassword).
				Value(&apiKey).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("API key cannot be empty")
					}
					return nil
				}),
		),
	)

	if err := promptHandler.HandleForm(ctx, keyForm); err != nil {
		return false, err
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

	if err := promptHandler.HandleForm(ctx, form); err != nil {
		return false, err
	}

	if !runSetup {
		return false, nil
	}

	return InteractiveSetup(ctx, promptHandler)
}
