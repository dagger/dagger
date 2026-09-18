package daggercmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/gqlerror"

	workspacepkg "github.com/dagger/dagger/core/workspace"
)

func TestWorkspaceSettingWriteValue(t *testing.T) {
	listSetting := workspaceSetting{Module: "vitest", Key: "tags", IsList: true}
	scalarSetting := workspaceSetting{Module: "aws", Key: "region"}

	t.Run("a single value for a scalar setting passes through unchanged", func(t *testing.T) {
		for _, value := range []string{"plain", "a,b", "[abc]*", ""} {
			got, values, err := workspaceSettingWriteValue(scalarSetting, []string{value})
			require.NoError(t, err)
			require.Equal(t, value, got)
			require.Nil(t, values)
		}
	})

	t.Run("a single value for a list setting becomes an explicit list", func(t *testing.T) {
		for value, want := range map[string][]string{
			".":                 {"."},
			"a,b":               {"a", "b"},
			"[.]":               {"."},
			"[a, b]":            {"a", "b"},
			`["a,b", "c"]`:      {"a,b", "c"},
			`["C:\foo"]`:        {`C:\foo`},
			"[abc]*":            {"[abc]*"},
			"smoke, regression": {"smoke", "regression"},
		} {
			got, values, err := workspaceSettingWriteValue(listSetting, []string{value})
			require.NoError(t, err, value)
			require.Empty(t, got, value)
			require.Equal(t, want, values, value)
		}
	})

	t.Run("an empty list writes the [] value with an empty explicit list", func(t *testing.T) {
		value, values, err := workspaceSettingWriteValue(listSetting, []string{"[]"})
		require.NoError(t, err)
		require.Equal(t, "[]", value)
		require.NotNil(t, values)
		require.Empty(t, values)
	})

	t.Run("malformed single values for a list setting fail", func(t *testing.T) {
		for value, wantErr := range map[string]string{
			"":          "list value is empty",
			"a,b,":      "list value has an empty element",
			`["a" "b"]`: "list value has unexpected text",
			`[a"b]`:     "list value has a stray quote",
		} {
			_, _, err := workspaceSettingWriteValue(listSetting, []string{value})
			require.ErrorContains(t, err, `setting "tags" of module "vitest" is a list: `+wantErr, value)
		}
	})

	t.Run("multiple values for a list setting pass as an explicit list", func(t *testing.T) {
		value, values, err := workspaceSettingWriteValue(listSetting, []string{"docs", "sdk/go"})
		require.NoError(t, err)
		require.Empty(t, value)
		require.Equal(t, []string{"docs", "sdk/go"}, values)
	})

	t.Run("elements with commas or brackets stay verbatim", func(t *testing.T) {
		value, values, err := workspaceSettingWriteValue(listSetting, []string{"a,b", `["c"]`})
		require.NoError(t, err)
		require.Empty(t, value)
		require.Equal(t, []string{"a,b", `["c"]`}, values)
	})

	t.Run("multiple values for a scalar setting fail", func(t *testing.T) {
		_, _, err := workspaceSettingWriteValue(scalarSetting, []string{"one", "two"})
		require.ErrorContains(t, err, `setting "region" of module "aws" is not a list and accepts a single value`)
	})

	t.Run("multiple values fail when isList is unset", func(t *testing.T) {
		_, _, err := workspaceSettingWriteValue(workspaceSetting{Module: "m", Key: "k"}, []string{"one", "two"})
		require.ErrorContains(t, err, "is not a list")
	})
}

func TestWorkspaceSettingDisplayValue(t *testing.T) {
	require.Equal(t, "us-west-2", workspaceSettingDisplayValue(workspaceSetting{Value: "us-west-2", DefaultValue: "us-east-1"}))
	require.Equal(t, "us-east-1 (default)", workspaceSettingDisplayValue(workspaceSetting{DefaultValue: "us-east-1"}))
	require.Equal(t, "", workspaceSettingDisplayValue(workspaceSetting{}))
}

func TestIsUndefinedEnvError(t *testing.T) {
	structured := &gqlerror.Error{
		Message: "something wrapped beyond recognition",
		Extensions: map[string]any{
			"_type": workspacepkg.UndefinedEnvErrorType,
			"env":   "dev",
		},
	}
	require.True(t, isUndefinedEnvError(fmt.Errorf("wrap: %w", structured), "dev"))
	// The structured match is authoritative: same _type, different env.
	require.False(t, isUndefinedEnvError(structured, "prod"))

	// Fallback for engines that don't attach extensions.
	plain := fmt.Errorf(`connect: workspace env "dev" is not defined (no envs defined)`)
	require.True(t, isUndefinedEnvError(plain, "dev"))
	require.False(t, isUndefinedEnvError(plain, "prod"))
	require.False(t, isUndefinedEnvError(nil, "dev"))
}

func TestWriteWorkspaceSettingsTableFitsViewWidth(t *testing.T) {
	settings := []workspaceSetting{
		{
			Module:      "module-with-a-name-that-is-too-long",
			Key:         "setting-with-a-key-that-is-too-long",
			Value:       strings.Repeat("value", 20),
			Description: strings.Repeat("A long description. ", 10),
		},
		{
			Module:      "short",
			Key:         "multiline",
			Value:       "first\nsecond\tthird",
			Description: "First description line.\nSecond description line.",
		},
	}

	var out bytes.Buffer
	require.NoError(t, writeWorkspaceSettingsTableAtWidth(&out, settings, 60, false))

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, len(settings)+1)
	for _, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), 60, line)
	}
	require.Contains(t, out.String(), "…")
	require.NotContains(t, out.String(), settings[0].Value)
	require.Contains(t, out.String(), "first second third")
	require.Contains(t, out.String(), "First description line.")
	require.NotContains(t, out.String(), "Second description line.")
}

func TestWriteWorkspaceSettingsTableWide(t *testing.T) {
	long := workspaceSetting{
		Module:      "module-with-a-name-that-is-too-long",
		Key:         "setting-with-a-key-that-is-too-long",
		Value:       strings.Repeat("value", 20),
		Description: strings.Repeat("A long description. ", 10),
	}
	for _, width := range []int{100, 60} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, writeWorkspaceSettingsTableAtWidth(&out, []workspaceSetting{long}, width, true))

			lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			// Rows wrap within the view instead of overflowing it, so a
			// terminal never soft-wraps them across columns.
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), width, line)
			}
			require.NotContains(t, out.String(), "…")

			// Every cell is shown whole: rejoining its wrapped column
			// restores it.
			starts := []int{0}
			for _, header := range workspaceSettingsHeaders[1:] {
				starts = append(starts, strings.Index(lines[0], header))
			}
			columns := make([]strings.Builder, len(starts))
			for _, line := range lines[1:] {
				for column, start := range starts {
					end := len(line)
					if column+1 < len(starts) {
						end = min(end, starts[column+1])
					}
					if start < end {
						columns[column].WriteString(strings.TrimSpace(line[start:end]))
						if column == len(starts)-1 {
							columns[column].WriteString(" ")
						}
					}
				}
			}
			require.Equal(t, long.Module, columns[0].String())
			require.Equal(t, long.Key, columns[1].String())
			require.Equal(t, long.Value, columns[2].String())
			require.Equal(t, strings.TrimSpace(long.Description), strings.TrimSpace(columns[3].String()))
		})
	}
}

func TestWriteWorkspaceSettingsTableWideMultiline(t *testing.T) {
	settings := []workspaceSetting{{
		Module:      "short",
		Key:         "multiline",
		Value:       "first\nsecond\tthird",
		Description: "First description line.\n\nSecond description line.",
	}}
	var out bytes.Buffer
	require.NoError(t, writeWorkspaceSettingsTableAtWidth(&out, settings, 100, true))
	require.Contains(t, out.String(), "first second third")
	require.Contains(t, out.String(), "First description line. Second description line.")
}

func TestWorkspaceSettingsWideOnlyLists(t *testing.T) {
	cmd := newSettingsCmd(false)
	require.NoError(t, cmd.Flags().Set("wide", "true"))
	t.Cleanup(func() { workspaceSettingsWide = false })

	err := runWorkspaceSettings(cmd, []string{"go", "version"})
	require.ErrorContains(t, err, "--wide applies to listing settings")
}

func TestWriteWorkspaceSettingsTableMeasuresUnicodeWidth(t *testing.T) {
	settings := []workspaceSetting{
		{
			Module:      "unicode",
			Key:         "emoji",
			Value:       strings.Repeat("界", 30),
			Description: strings.Repeat("Configuration 🚀 ", 10),
		},
	}

	var out bytes.Buffer
	require.NoError(t, writeWorkspaceSettingsTableAtWidth(&out, settings, 48, false))

	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 48, line)
		require.True(t, utf8.ValidString(line))
	}
	require.Contains(t, out.String(), "…")
}
