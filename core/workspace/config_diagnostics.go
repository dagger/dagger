package workspace

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml"
)

// CheckConfigFields reports workspace fields that the schema does not define.
// Unknown fields are errors. Deprecated workspace fields and legacy SDK fields
// are only warnings, so that old configs can still be loaded and migrated.
// Settings maps belong to modules and SDKs, so their contents are unrestricted.
func CheckConfigFields(data []byte, filename string) (warnings []string, _ error) {
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	type unknownField struct {
		pos  toml.Position
		path string
	}
	var unknown []unknownField
	var visit func(*toml.Tree, reflect.Type, []string)
	visit = func(tree *toml.Tree, typ reflect.Type, prefix []string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		fields := map[string]reflect.Type{}
		if typ.Kind() == reflect.Struct {
			for i := range typ.NumField() {
				field := typ.Field(i)
				name := strings.Split(field.Tag.Get("toml"), ",")[0]
				if name != "" && name != "-" {
					// Match go-toml's field lookup order, including accepted
					// case variants. Only the first present key is consumed.
					if key, ok := configDecoderKey(tree, name); ok {
						fields[key] = field.Type
					}
				}
			}
		}
		for _, key := range sortedConfigKeys(tree) {
			parts := append(slices.Clone(prefix), key)
			childType, known := fields[key]
			if typ.Kind() == reflect.Map {
				childType, known = typ.Elem(), true
			}
			if legacyWorkspaceMainPath(parts) {
				pos := tree.GetPositionPath([]string{key})
				warnings = append(warnings, fmt.Sprintf("%s:%d:%d: deprecated field %s is accepted for compatibility; use %s", filename, pos.Line, pos.Col, JoinConfigPath(parts...), JoinConfigPath(parts[:2]...)+".main"))
			}
			if !known {
				pos := tree.GetPositionPath([]string{key})
				if legacySDKConfigPath(parts) {
					warnings = append(warnings, fmt.Sprintf("%s:%d:%d: unsupported field %s is ignored; run `dagger ws migrate` to migrate it", filename, pos.Line, pos.Col, JoinConfigPath(parts...)))
				} else {
					unknown = append(unknown, unknownField{pos, JoinConfigPath(parts...)})
				}
				continue
			}
			switch child := tree.GetPath([]string{key}).(type) {
			case *toml.Tree:
				if childType.Kind() != reflect.Interface {
					visit(child, childType, parts)
				}
			case []*toml.Tree:
				if childType.Kind() == reflect.Slice {
					for _, elem := range child {
						visit(elem, childType.Elem(), parts)
					}
				}
			}
		}
	}
	visit(tree, reflect.TypeFor[Config](), nil)
	// Report errors in file order.
	slices.SortStableFunc(unknown, func(a, b unknownField) int {
		return cmp.Or(cmp.Compare(a.pos.Line, b.pos.Line), cmp.Compare(a.pos.Col, b.pos.Col))
	})
	errs := make([]error, 0, len(unknown))
	for _, field := range unknown {
		errs = append(errs, fmt.Errorf("%s:%d:%d: unknown field %s", filename, field.pos.Line, field.pos.Col, field.path))
	}
	return warnings, errors.Join(errs...)
}

func legacyWorkspaceMainPath(parts []string) bool {
	return len(parts) == 3 && configDecoderKeyMatches(parts[0], "modules") && configDecoderKeyMatches(parts[2], "entrypoint")
}

// legacySDKConfigPath reports whether parts names a legacy SDK field that
// `dagger ws migrate` can migrate. Environment-specific SDK roles cannot be
// migrated, so they are not included.
func legacySDKConfigPath(parts []string) bool {
	return len(parts) == 3 && configDecoderKeyMatches(parts[0], "modules") && configDecoderKeyMatches(parts[2], "as-sdk")
}

// configDecoderKey returns the key go-toml consumes for a tagged struct field.
// Keep this order synchronized with go-toml's Decoder.valueFromTree.
func configDecoderKey(tree *toml.Tree, name string) (string, bool) {
	if tree == nil {
		return "", false
	}
	for _, key := range configDecoderKeys(name) {
		if tree.HasPath([]string{key}) {
			return key, true
		}
	}
	return "", false
}

func configDecoderKeyMatches(key, name string) bool {
	return slices.Contains(configDecoderKeys(name), key)
}

func configDecoderKeys(name string) []string {
	return []string{name, strings.ToLower(name), strings.ToTitle(name), strings.ToLower(name[:1]) + name[1:]}
}
