package gitutil

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsCommitSHA(t *testing.T) {
	for truthy, commits := range map[bool][]string{
		true: {
			"01234567890abcdef01234567890abcdef012345", // 40 valid characters (SHA-1)
		},
		false: {
			"",       // empty string
			"abcdef", // too short

			"123456789012345678901234567890123456789",   // 39 valid characters
			"!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!",  // 40 invalid characters
			"12345678901234567890123456789012345678901", // 41 valid characters

			"01234567890abcdef01234567890abcdef01234567890abcdef01234567890a",   // 63 valid characters
			"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",  // 64 invalid characters
			"01234567890abcdef01234567890abcdef01234567890abcdef01234567890abc", // 65 valid characters

			// TODO: add SHA-256 support and move this up to the "true" section
			"01234567890abcdef01234567890abcdef01234567890abcdef01234567890ab", // 64 valid characters (SHA-256)
		},
	} {
		for _, commit := range commits {
			t.Run(fmt.Sprintf("%t/%q", truthy, commit), func(t *testing.T) {
				assert.Equal(t, truthy, IsCommitSHA(commit))
			})
		}
	}
}
