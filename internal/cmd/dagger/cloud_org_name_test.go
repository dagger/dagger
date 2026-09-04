package daggercmd

import (
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

func TestSanitizeOrgName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"notnotvito", "notnotvito"},
		{"NotNotVito", "notnotvito"},
		{"  Marcos Nils  ", "marcos-nils"},
		{"foo_bar.baz", "foo-bar-baz"},
		{"--weird__name--", "weird-name"},
		{"a@b", "ab"},
		{"", ""},
		{"!!!", ""},
		{"123", "123"},
	} {
		require.Equal(t, tc.want, sanitizeOrgName(tc.in), "sanitizeOrgName(%q)", tc.in)
	}
}

func TestDefaultOrgName(t *testing.T) {
	for _, tc := range []struct {
		name string
		user *cloudapi.UserResponse
		want string
	}{
		{
			name: "prefers nickname",
			user: &cloudapi.UserResponse{Nickname: "notnotvito", Email: "vito@example.com"},
			want: "notnotvito",
		},
		{
			name: "falls back to email local part",
			user: &cloudapi.UserResponse{Email: "marcos.nils@example.com"},
			want: "marcos-nils",
		},
		{
			name: "falls back to generic when nothing usable",
			user: &cloudapi.UserResponse{Nickname: "!!!", Email: "@example.com"},
			want: "my-org",
		},
		{
			name: "nil user",
			user: nil,
			want: "my-org",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, defaultOrgName(tc.user))
		})
	}
}
