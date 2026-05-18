package core

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

var ErrBuiltinModuleNotFound = errors.New("builtin module source not found")

type BuiltinModuleSource struct {
	Name              string        `field:"true" name:"name" doc:"Stable builtin module catalog name."`
	Description       string        `field:"true" name:"description" doc:"Human-readable builtin module description."`
	ManifestDigest    digest.Digest `json:"manifestDigest,omitempty"`
	SourceRootSubpath string        `json:"sourceRootSubpath,omitempty"`
	FullRootfsSubpath string        `json:"fullRootfsSubpath,omitempty"`
	OriginalRootfs    dagql.ObjectResult[*Directory]
}

func (*BuiltinModuleSource) Type() *ast.Type {
	return &ast.Type{
		NamedType: "BuiltinModuleSource",
		NonNull:   true,
	}
}

func (*BuiltinModuleSource) TypeDescription() string {
	return "An engine-bundled module source catalog entry."
}

func (src BuiltinModuleSource) Clone() *BuiltinModuleSource {
	cp := src
	return &cp
}

type BuiltinModuleCatalogEntry struct {
	Name              string
	Description       string
	Source            string
	ManifestDigest    digest.Digest
	Subpath           string
	FullRootfsSubpath string
	Internal          bool
	Aliases           []string
}

type BuiltinModuleCatalog struct {
	entries []BuiltinModuleCatalogEntry
}

func NewBuiltinModuleCatalog(entries []BuiltinModuleCatalogEntry) *BuiltinModuleCatalog {
	cloned := slices.Clone(entries)
	for i := range cloned {
		cloned[i].Aliases = slices.Clone(cloned[i].Aliases)
	}
	return &BuiltinModuleCatalog{entries: cloned}
}

func DefaultBuiltinModuleCatalog() *BuiltinModuleCatalog {
	return NewBuiltinModuleCatalog([]BuiltinModuleCatalogEntry{
		{
			Name:           "python-runtime",
			Description:    "Builtin Python SDK runtime module",
			Source:         "python",
			ManifestDigest: digest.Digest(os.Getenv(distconsts.PythonSDKManifestDigestEnvName)),
			Subpath:        "runtime",
		},
		{
			Name:           "typescript-runtime",
			Description:    "Builtin TypeScript SDK runtime module",
			Source:         "typescript",
			ManifestDigest: digest.Digest(os.Getenv(distconsts.TypescriptSDKManifestDigestEnvName)),
			Subpath:        "runtime",
		},
		{
			Name:        "java-runtime",
			Description: "Builtin Java SDK runtime module",
			Source:      "github.com/dagger/dagger/sdk/java",
		},
		{
			Name:        "php-runtime",
			Description: "Builtin PHP SDK runtime module",
			Source:      "github.com/dagger/dagger/sdk/php",
		},
		{
			Name:        "elixir-runtime",
			Description: "Builtin Elixir SDK runtime module",
			Source:      "github.com/dagger/dagger/sdk/elixir",
		},
	})
}

func (c *BuiltinModuleCatalog) Lookup(name string) (BuiltinModuleCatalogEntry, error) {
	if c == nil {
		c = DefaultBuiltinModuleCatalog()
	}
	for _, entry := range c.entries {
		if entry.Name == name || slices.Contains(entry.Aliases, name) {
			if err := entry.Validate(); err != nil {
				return BuiltinModuleCatalogEntry{}, err
			}
			return entry, nil
		}
	}
	return BuiltinModuleCatalogEntry{}, fmt.Errorf("%w: %q", ErrBuiltinModuleNotFound, name)
}

func (c *BuiltinModuleCatalog) List() ([]BuiltinModuleCatalogEntry, error) {
	if c == nil {
		c = DefaultBuiltinModuleCatalog()
	}
	entries := make([]BuiltinModuleCatalogEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		if entry.Internal {
			continue
		}
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		entry.Aliases = slices.Clone(entry.Aliases)
		entries = append(entries, entry)
	}
	return entries, nil
}

func (entry BuiltinModuleCatalogEntry) Validate() error {
	if entry.Name == "" {
		return fmt.Errorf("builtin module catalog entry has empty name")
	}
	if entry.Source == "" {
		return fmt.Errorf("builtin module %q has empty source", entry.Name)
	}
	if entry.ManifestDigest != "" {
		if err := entry.ManifestDigest.Validate(); err != nil {
			return fmt.Errorf("builtin module %q has invalid manifest digest %q: %w", entry.Name, entry.ManifestDigest, err)
		}
		if entry.Subpath == "" {
			return fmt.Errorf("builtin module %q has empty subpath", entry.Name)
		}
	}
	return nil
}

func (entry BuiltinModuleCatalogEntry) ModuleSourceMetadata() *BuiltinModuleSource {
	return &BuiltinModuleSource{
		Name:              entry.Name,
		Description:       entry.Description,
		ManifestDigest:    entry.ManifestDigest,
		SourceRootSubpath: entry.Subpath,
		FullRootfsSubpath: entry.FullRootfsSubpath,
	}
}
