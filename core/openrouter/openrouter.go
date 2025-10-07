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

var daggerToOpenRouter = map[string]string{
	"claude-sonnet-4-5": "anthropic/claude-sonnet-4.5",
	"claude-sonnet-4-0": "anthropic/claude-sonnet-4",
}

func (m Models) Lookup(idOrName string) *Model {
	if converted, ok := daggerToOpenRouter[idOrName]; ok {
		idOrName = converted
	}
	for _, model := range m {
		if model.ID == idOrName {
			return &model
		}
		provider, name, _ := strings.Cut(model.ID, "/")
		switch provider {
		case "anthropic", "openai", "google":
			// Only do this for grandfathered-in models before we switched to explicit
			// IDs.
			if name == idOrName {
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
