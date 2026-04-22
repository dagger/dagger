package main

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestIsWorkspaceSettingType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		typeDef      *modTypeDef
		configurable bool
	}{
		{
			name:         "primitive",
			typeDef:      testStringTypeDef(),
			configurable: true,
		},
		{
			name:         "address backed object",
			typeDef:      testObjectTypeDef(Secret, "", ""),
			configurable: true,
		},
		{
			name:         "workspace object",
			typeDef:      testObjectTypeDef("Workspace", "", ""),
			configurable: false,
		},
		{
			name:         "non address backed object",
			typeDef:      testObjectTypeDef(CacheVolume, "", ""),
			configurable: false,
		},
		{
			name: "primitive list",
			typeDef: &modTypeDef{
				Kind:   dagger.TypeDefKindListKind,
				AsList: &modList{ElementTypeDef: testStringTypeDef()},
			},
			configurable: true,
		},
		{
			name: "object list",
			typeDef: &modTypeDef{
				Kind:   dagger.TypeDefKindListKind,
				AsList: &modList{ElementTypeDef: testObjectTypeDef(Secret, "", "")},
			},
			configurable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.configurable, isWorkspaceSettingType(tt.typeDef))
		})
	}
}
