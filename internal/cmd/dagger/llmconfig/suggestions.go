package llmconfig

import (
	"encoding/json"
	"os/exec"
)

// opListVaults returns vault names via `op vault list`.
func opListVaults() []string {
	if _, err := exec.LookPath("op"); err != nil {
		return nil
	}

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

	names := make([]string, 0, len(vaults))
	for _, v := range vaults {
		names = append(names, v.Name)
	}
	return names
}

// opListItems returns item titles in a vault via `op item list`.
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

	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Title)
	}
	return names
}

// opListFields returns field labels for an item via `op item get`.
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

	names := make([]string, 0, len(result.Fields))
	for _, f := range result.Fields {
		if f.Label != "" {
			names = append(names, f.Label)
		}
	}
	return names
}
