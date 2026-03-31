package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const modelsURL = "https://openrouter.ai/api/v1/models"

func FetchModels(ctx context.Context) (Models, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", modelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Dagger-Client/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var mr ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return nil, err
	}
	return mr.Data, nil
}

type ModelsResponse struct {
	Data Models `json:"data"`
}

type Models []Model

type Model struct {
	ID              string       `json:"id"`
	CanonicalSlug   string       `json:"canonical_slug"`
	HuggingFaceID   string       `json:"hugging_face_id"`
	Name            string       `json:"name"`
	Created         int64        `json:"created"`
	Description     string       `json:"description"`
	ContextLength   int64        `json:"context_length"`
	Architecture    Architecture `json:"architecture"`
	Pricing         Pricing      `json:"pricing"`
	TopProvider     TopProvider  `json:"top_provider"`
	SupportedParams []string     `json:"supported_parameters"`
}

// knownProviderPrefixes maps model name prefixes to OpenRouter provider slugs.
var knownProviderPrefixes = []struct {
	prefix   string
	provider string
}{
	{"claude-", "anthropic"},
	{"gpt-", "openai"},
	{"o1-", "openai"},
	{"o3-", "openai"},
	{"o4-", "openai"},
	{"gemini-", "google"},
}

// daggerToOpenRouter converts a Dagger-style model name (no provider prefix,
// hyphens for version separators) into an OpenRouter model ID
// (provider/name with dots for version separators).
//
// Examples:
//
//	claude-sonnet-4-5       → anthropic/claude-sonnet-4.5
//	claude-sonnet-4-0       → anthropic/claude-sonnet-4
//	gpt-5-2                 → openai/gpt-5.2
//	gemini-2-5-flash        → google/gemini-2.5-flash
//	anthropic/claude-opus-4 → anthropic/claude-opus-4 (already qualified)
func daggerToOpenRouter(name string) string {
	// Already has a provider prefix — leave it alone.
	if strings.Contains(name, "/") {
		return name
	}

	// Detect provider from known prefixes.
	provider := ""
	for _, pp := range knownProviderPrefixes {
		if strings.HasPrefix(name, pp.prefix) {
			provider = pp.provider
			break
		}
	}
	if provider == "" {
		return name // unknown provider, return as-is
	}

	// Convert version-style hyphens to dots.
	// Walk the parts split by "-" and join digit-digit boundaries with "."
	// instead of "-", but drop trailing "-0" (e.g. "4-0" → "4").
	//
	// "claude-sonnet-4-5"  → ["claude","sonnet","4","5"]  → "claude-sonnet-4.5"
	// "gemini-2-5-flash"   → ["gemini","2","5","flash"]   → "gemini-2.5-flash"
	// "gpt-5-2-chat"       → ["gpt","5","2","chat"]       → "gpt-5.2-chat"
	// "claude-sonnet-4-0"  → ["claude","sonnet","4","0"]   → "claude-sonnet-4"
	parts := strings.Split(name, "-")
	var result []string
	for i, part := range parts {
		if i > 0 && isDigit(parts[i-1]) && isDigit(part) {
			// Digit follows digit: use dot separator.
			// Drop trailing ".0" — "4-0" becomes just "4".
			if part == "0" && (i == len(parts)-1 || !isDigit(safeGet(parts, i+1))) {
				continue
			}
			result[len(result)-1] += "." + part
		} else {
			result = append(result, part)
		}
	}

	return provider + "/" + strings.Join(result, "-")
}

func isDigit(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func safeGet(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

func (m Models) Lookup(idOrName string) *Model {
	converted := daggerToOpenRouter(idOrName)
	for _, model := range m {
		if model.ID == converted {
			return &model
		}
		if model.ID == idOrName {
			return &model
		}
		provider, name, _ := strings.Cut(model.ID, "/")
		switch provider {
		case "anthropic", "openai", "google":
			// Only do this for grandfathered-in models before we switched to explicit
			// IDs.
			if name == idOrName || name == converted {
				return &model
			}
		default:
			continue
		}
	}
	return nil
}

type Architecture struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Tokenizer        string   `json:"tokenizer"`
	InstructType     *string  `json:"instruct_type"`
}

type Pricing struct {
	Prompt            PricePerToken `json:"prompt"`
	Completion        PricePerToken `json:"completion"`
	Request           PricePerToken `json:"request"`
	Image             PricePerToken `json:"image"`
	WebSearch         PricePerToken `json:"web_search"`
	InternalReasoning PricePerToken `json:"internal_reasoning"`
	InputCacheRead    PricePerToken `json:"input_cache_read"`
	InputCacheWrite   PricePerToken `json:"input_cache_write"`
}

type PricePerToken float64

func (p PricePerToken) Cost(tokens int) float64 {
	return float64(p) * float64(tokens)
}

func (p PricePerToken) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatFloat(float64(p), 'f', -1, 64))
}

func (p *PricePerToken) UnmarshalJSON(data []byte) error {
	var price string
	if err := json.Unmarshal(data, &price); err != nil {
		return err
	}
	perToken, err := strconv.ParseFloat(price, 64)
	if err != nil {
		return err
	}
	*p = PricePerToken(perToken)
	return nil
}

func (p PricePerToken) CostPer1MIn() float64 {
	return float64(p)
}

type TopProvider struct {
	ContextLength       int64  `json:"context_length"`
	MaxCompletionTokens *int64 `json:"max_completion_tokens"`
	IsModerated         bool   `json:"is_moderated"`
}

type Endpoint struct {
	Name                string   `json:"name"`
	ContextLength       int64    `json:"context_length"`
	Pricing             Pricing  `json:"pricing"`
	ProviderName        string   `json:"provider_name"`
	Tag                 string   `json:"tag"`
	Quantization        *string  `json:"quantization"`
	MaxCompletionTokens *int64   `json:"max_completion_tokens"`
	MaxPromptTokens     *int64   `json:"max_prompt_tokens"`
	SupportedParams     []string `json:"supported_parameters"`
	Status              int      `json:"status"`
	UptimeLast30m       float64  `json:"uptime_last_30m"`
}
