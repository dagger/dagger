package netrc

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetrcParsing(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []NetrcEntry
	}{
		{
			name:     "Empty file",
			content:  ``,
			expected: []NetrcEntry(nil),
		},
		{
			name:    "Single entry",
			content: `machine example.com login user1 password pass1`,
			expected: []NetrcEntry{
				{Machine: "example.com", Login: "user1", Password: "pass1"},
			},
		},
		{
			name: "Multiple entries",
			content: `machine example.com login user1 password pass1
machine test.com login user2 password pass2`,
			expected: []NetrcEntry{
				{Machine: "example.com", Login: "user1", Password: "pass1"},
				{Machine: "test.com", Login: "user2", Password: "pass2"},
			},
		},
		{
			name:    "Entry with default",
			content: `default login defaultuser password defaultpass`,
			expected: []NetrcEntry{
				{Machine: "", Login: "defaultuser", Password: "defaultpass"},
			},
		},
		{
			name: "Entry with macdef",
			content: `machine example.com login user1 password pass1
macdef init
echo "This is a macro"
machine wrong.com login user2 password pass2

machine right.com login user3 password pass3`,
			expected: []NetrcEntry{
				{Machine: "example.com", Login: "user1", Password: "pass1"},
				{Machine: "right.com", Login: "user3", Password: "pass3"},
			},
		},
		{
			name: "Entry with newlines",
			content: `machine example.com
	login user1
	password pass1`,
			expected: []NetrcEntry{
				{Machine: "example.com", Login: "user1", Password: "pass1"},
			},
		},
		{
			name:    "Entry with quotes",
			content: `machine "example.com" login "user1 space" password "pass1 space"`,
			expected: []NetrcEntry{
				{Machine: "example.com", Login: "user1 space", Password: "pass1 space"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := slices.Collect(NetrcEntries(strings.NewReader(tt.content)))
			require.Equal(t, tt.expected, entries)
		})
	}
}
