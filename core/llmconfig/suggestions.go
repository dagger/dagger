package llmconfig

import (
	"encoding/json"
	"os/exec"
	"strings"
)

// SecretSchemes returns the supported secret provider URI schemes.
var SecretSchemes = []string{
	"op://",
	"vault://",
	"env://",
	"cmd://",
	"file://",
	"aws+sm://",
	"aws+ps://",
	"libsecret://",
}

// SecretSuggestions returns autocomplete suggestions for the API key input.
// It provides secret provider URI schemes and, when `op` CLI is available,
// 1Password vault/item completions.
func SecretSuggestions(current string) []string {
	// If user hasn't typed a scheme yet, suggest all schemes
	if !strings.Contains(current, "://") {
		return SecretSchemes
	}

	// If user typed an op:// prefix, complete with 1Password vaults/items
	if strings.HasPrefix(current, "op://") {
		return opSuggestions(current)
	}

	return nil
}

// opSuggestions returns 1Password URI completions using the `op` CLI.
// URIs follow the form: op://vault/item/field
func opSuggestions(current string) []string {
	if _, err := exec.LookPath("op"); err != nil {
		return nil
	}

	path := strings.TrimPrefix(current, "op://")
	parts := strings.SplitN(path, "/", 3)

	switch {
	case len(parts) <= 1:
		// No slash yet or completing vault name — list vaults
		return opListVaults()
	case len(parts) == 2:
		// Have vault, completing item name — list items in vault
		return opListItems(parts[0])
	case len(parts) == 3:
		// Have vault/item, completing field name — list fields
		return opListFields(parts[0], parts[1])
	}

	return nil
}

// opListVaults returns op://vault suggestions.
func opListVaults() []string {
	out, err := exec.Command("op", "vault", "list", "--format=json").Output()
	if err != nil {
		return nil
	}

	var vaults []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &vaults); err != nil {
		return nil
	}

	suggestions := make([]string, 0, len(vaults))
	for _, v := range vaults {
		suggestions = append(suggestions, "op://"+v.Name+"/")
	}
	return suggestions
}

// opListItems returns op://vault/item suggestions.
func opListItems(vault string) []string {
	out, err := exec.Command("op", "item", "list", "--vault="+vault, "--format=json").Output()
	if err != nil {
		return nil
	}

	var items []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return nil
	}

	prefix := "op://" + vault + "/"
	suggestions := make([]string, 0, len(items))
	for _, item := range items {
		suggestions = append(suggestions, prefix+item.Title+"/")
	}
	return suggestions
}

// opListFields returns op://vault/item/field suggestions.
func opListFields(vault, item string) []string {
	out, err := exec.Command("op", "item", "get", item, "--vault="+vault, "--format=json").Output()
	if err != nil {
		return nil
	}

	var result struct {
		Fields []struct {
			Label string `json:"label"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil
	}

	prefix := "op://" + vault + "/" + item + "/"
	suggestions := make([]string, 0, len(result.Fields))
	for _, f := range result.Fields {
		if f.Label != "" {
			suggestions = append(suggestions, prefix+f.Label)
		}
	}
	return suggestions
}
