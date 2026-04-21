package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSettingHintTypeInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		typeDef      *core.TypeDef
		configurable bool
	}{
		{
			name:         "primitive",
			typeDef:      &core.TypeDef{Kind: core.TypeDefKindString},
			configurable: true,
		},
		{
			name:         "address backed object",
			typeDef:      objectTypeDef("Secret"),
			configurable: true,
		},
		{
			name:         "workspace object",
			typeDef:      objectTypeDef("Workspace"),
			configurable: false,
		},
		{
			name:         "non address backed object",
			typeDef:      objectTypeDef("CacheVolume"),
			configurable: false,
		},
		{
			name:         "primitive list",
			typeDef:      (&core.TypeDef{}).WithListOf(&core.TypeDef{Kind: core.TypeDefKindString}),
			configurable: true,
		},
		{
			name:         "object list",
			typeDef:      (&core.TypeDef{}).WithListOf(objectTypeDef("Secret")),
			configurable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, configurable := typeInfoFromTypeDef(tt.typeDef)
			require.Equal(t, tt.configurable, configurable)
		})
	}
}

func objectTypeDef(name string) *core.TypeDef {
	return (&core.TypeDef{}).WithObject(name, "", nil, nil)
}
