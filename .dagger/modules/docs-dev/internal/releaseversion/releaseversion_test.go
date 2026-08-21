package releaseversion

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ref     string
		version string
		wantErr string
	}{
		{
			name:    "long tag ref with prefix",
			ref:     "refs/tags/v1.0.0-beta.9",
			version: "1.0.0-beta.9",
		},
		{
			name:    "long tag ref without prefix",
			ref:     "refs/tags/1.2.3",
			version: "1.2.3",
		},
		{
			name:    "long branch ref",
			ref:     "refs/heads/v2.0.0",
			version: "2.0.0",
		},
		{
			name:    "short ref",
			ref:     "v3.4.5+build.6",
			version: "3.4.5+build.6",
		},
		{
			name:    "invalid version",
			ref:     "refs/tags/latest",
			wantErr: `release ref "refs/tags/latest" is not a semantic version`,
		},
		{
			name:    "commit",
			ref:     "0123456789abcdef0123456789abcdef01234567",
			wantErr: `release ref "0123456789abcdef0123456789abcdef01234567" is not a semantic version`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			version, err := Parse(tt.ref)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Parse(%q) error = %v, want %q", tt.ref, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.ref, err)
			}
			if version != tt.version {
				t.Errorf("Parse(%q) = %q, want %q", tt.ref, version, tt.version)
			}
		})
	}
}
