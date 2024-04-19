package linter

import (
	"fmt"
)

var (
	RuleStageNameCasing = LinterRule[func(string) string]{
		Name:        "StageNameCasing",
		Description: "Stage names should be lowercase",
		Format: func(stageName string) string {
			return fmt.Sprintf("Stage name '%s' should be lowercase", stageName)
		},
	}
	RuleFromAsCasing = LinterRule[func(string, string) string]{
		Name:        "FromAsCasing",
		Description: "The 'as' keyword should match the case of the 'from' keyword",
		Format: func(from, as string) string {
			return fmt.Sprintf("'%s' and '%s' keywords' casing do not match", as, from)
		},
	}
	RuleNoEmptyContinuations = LinterRule[func() string]{
		Name:        "NoEmptyContinuations",
		Description: "Empty continuation lines will become errors in a future release",
		URL:         "https://github.com/moby/moby/pull/33719",
		Format: func() string {
			return "Empty continuation line"
		},
	}
	RuleSelfConsistentCommandCasing = LinterRule[func(string) string]{
		Name:        "SelfConsistentCommandCasing",
		Description: "Commands should be in consistent casing (all lower or all upper)",
		Format: func(command string) string {
			return fmt.Sprintf("Command '%s' should be consistently cased", command)
		},
	}
	RuleFileConsistentCommandCasing = LinterRule[func(string, string) string]{
		Name:        "FileConsistentCommandCasing",
		Description: "All commands within the Dockerfile should use the same casing (either upper or lower)",
		Format: func(violatingCommand, correctCasing string) string {
			return fmt.Sprintf("Command '%s' should match the case of the command majority (%s)", violatingCommand, correctCasing)
		},
	}
)
