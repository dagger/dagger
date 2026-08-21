package secretprovider

import (
	"testing"
	"time"
)

func TestSplitVaultKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		wantPath  string
		wantField string
		wantErr   bool
	}{
		{
			name:      "simple key",
			key:       "my-app.token",
			wantPath:  "my-app",
			wantField: "token",
		},
		{
			name:      "nested path",
			key:       "path/to/secret.credential",
			wantPath:  "path/to/secret",
			wantField: "credential",
		},
		{
			name:      "field with dot in path (last dot splits)",
			key:       "foo.bar.baz",
			wantPath:  "foo.bar",
			wantField: "baz",
		},
		{
			name:    "no separator",
			key:     "single",
			wantErr: true,
		},
		{
			name:      "starts with dot (path is empty)",
			key:       ".token",
			wantPath:  "",
			wantField: "token",
		},
		{
			name:    "empty string",
			key:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, field, err := splitVaultKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Errorf("splitVaultKey(%q) expected error, got nil", tt.key)
				}
				return
			}
			if err != nil {
				t.Errorf("splitVaultKey(%q) unexpected error: %v", tt.key, err)
				return
			}
			if path != tt.wantPath {
				t.Errorf("splitVaultKey(%q) path = %q, want %q", tt.key, path, tt.wantPath)
			}
			if field != tt.wantField {
				t.Errorf("splitVaultKey(%q) field = %q, want %q", tt.key, field, tt.wantField)
			}
		})
	}
}

func TestParseVaultFullKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		wantMount string
		wantPath  string
		wantField string
		wantErr   bool
	}{
		{
			name:      "simple full path",
			key:       "my-engine/my-app.token",
			wantMount: "my-engine",
			wantPath:  "my-app",
			wantField: "token",
		},
		{
			name:      "nested full path",
			key:       "secret/foo/path/to/secret.credential",
			wantMount: "secret",
			wantPath:  "foo/path/to/secret",
			wantField: "credential",
		},
		{
			name:      "full path with multiple fields (last dot splits)",
			key:       "my-engine/foo.bar.baz",
			wantMount: "my-engine",
			wantPath:  "foo.bar",
			wantField: "baz",
		},
		{
			name:    "no slash (missing mount)",
			key:     "my-app.token",
			wantErr: true,
		},
		{
			name:    "empty string",
			key:     "",
			wantErr: true,
		},
		{
			name:    "no field separator",
			key:     "my-engine/single",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mount, path, field, err := parseVaultFullKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseVaultFullKey(%q) expected error, got nil", tt.key)
				}
				return
			}
			if err != nil {
				t.Errorf("parseVaultFullKey(%q) unexpected error: %v", tt.key, err)
				return
			}
			if mount != tt.wantMount {
				t.Errorf("parseVaultFullKey(%q) mount = %q, want %q", tt.key, mount, tt.wantMount)
			}
			if path != tt.wantPath {
				t.Errorf("parseVaultFullKey(%q) path = %q, want %q", tt.key, path, tt.wantPath)
			}
			if field != tt.wantField {
				t.Errorf("parseVaultFullKey(%q) field = %q, want %q", tt.key, field, tt.wantField)
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
