package secretprovider

import (
	"testing"
	"time"
)

func TestSplitVaultPath(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantMount  string
		wantSecret string
		wantField  string
		wantErr    bool
	}{
		{
			name:       "simple path",
			path:       "my-app/token",
			wantMount:  "my-app",
			wantSecret: "token",
			wantField:  "",
			wantErr:    true,
		},
		{
			name:       "mount with field",
			path:       "my-app/secret.token",
			wantMount:  "my-app",
			wantSecret: "secret",
			wantField:  "token",
		},
		{
			name:       "nested secret path with field",
			path:       "kv/path/to/secret.credential",
			wantMount:  "kv",
			wantSecret: "path/to/secret",
			wantField:  "credential",
		},
		{
			name:    "field with dot in path (last dot splits)",
			path:    "foo.bar.baz.qux",
			wantErr: true,
		},
		{
			name:    "no separator slash",
			path:    "single",
			wantErr: true,
		},
		{
			name:    "starts with slash (empty mount)",
			path:    "/.token",
			wantErr: true,
		},
		{
			name:       "empty mount after slash",
			path:       "/secret.field",
			wantMount:  "",
			wantSecret: "secret",
			wantField:  "field",
			wantErr:    true,
		},
		{
			name:    "empty string",
			path:    "",
			wantErr: true,
		},
		{
			name:    "slash but no dot",
			path:    "mount/secret",
			wantErr: true,
		},
		{
			name:       "secret path with dot in name",
			path:       "kv/data/config.v2.api_key",
			wantMount:  "kv",
			wantSecret: "data/config.v2",
			wantField:  "api_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mount, secretPath, secretField, err := splitVaultPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("splitVaultPath(%q) expected error, got nil", tt.path)
				}
				return
			}
			if err != nil {
				t.Errorf("splitVaultPath(%q) unexpected error: %v", tt.path, err)
				return
			}
			if mount != tt.wantMount {
				t.Errorf("splitVaultPath(%q) mount = %q, want %q", tt.path, mount, tt.wantMount)
			}
			if secretPath != tt.wantSecret {
				t.Errorf("splitVaultPath(%q) secretPath = %q, want %q", tt.path, secretPath, tt.wantSecret)
			}
			if secretField != tt.wantField {
				t.Errorf("splitVaultPath(%q) secretField = %q, want %q", tt.path, secretField, tt.wantField)
			}
		})
	}
}

func TestHasExpired(t *testing.T) {
	tests := []struct {
		name     string
		input    dataWithTTL
		wantBool bool
	}{
		{
			name:     "no ttl set (zero time)",
			input:    dataWithTTL{},
			wantBool: false,
		},
		{
			name:     "not expired",
			input:    dataWithTTL{expiresAt: time.Now().Add(time.Hour)},
			wantBool: false,
		},
		{
			name:     "expired",
			input:    dataWithTTL{expiresAt: time.Now().Add(-time.Hour)},
			wantBool: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasExpired(tt.input)
			if got != tt.wantBool {
				t.Errorf("hasExpired() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}
