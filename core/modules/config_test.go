package modules_test

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/core/modules"
)

func TestModuleCodegenConfig_Validate(t *testing.T) {
	tru := true
	fal := false

	cases := []struct {
		name    string
		cfg     *modules.ModuleCodegenConfig
		wantErr string // empty means no error expected
	}{
		{
			name:    "nil config is valid",
			cfg:     nil,
			wantErr: "",
		},
		{
			name: "both nil is valid (legacy default)",
			cfg:  &modules.ModuleCodegenConfig{},
		},
		{
			name: "legacyCodegenAtRuntime=true, automaticGitignore=true is valid",
			cfg: &modules.ModuleCodegenConfig{
				AutomaticGitignore:     &tru,
				LegacyCodegenAtRuntime: &tru,
			},
		},
		{
			name: "legacyCodegenAtRuntime=false, automaticGitignore=false is valid",
			cfg: &modules.ModuleCodegenConfig{
				AutomaticGitignore:     &fal,
				LegacyCodegenAtRuntime: &fal,
			},
		},
		{
			name: "legacyCodegenAtRuntime=false, automaticGitignore=nil is invalid",
			cfg: &modules.ModuleCodegenConfig{
				LegacyCodegenAtRuntime: &fal,
			},
			wantErr: "automaticGitignore=false",
		},
		{
			name: "legacyCodegenAtRuntime=false, automaticGitignore=true is invalid",
			cfg: &modules.ModuleCodegenConfig{
				AutomaticGitignore:     &tru,
				LegacyCodegenAtRuntime: &fal,
			},
			wantErr: "automaticGitignore=false",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected no error, got: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}
