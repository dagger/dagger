package workspace

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml"
)

// ConfigWarnings reports unsupported workspace fields without interpreting them.
// Settings maps belong to modules and SDKs, so their contents are unrestricted.
func ConfigWarnings(data []byte, filename string) ([]string, error) {
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	var warnings []string
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
			if !known {
				pos := tree.GetPositionPath([]string{key})
				message := fmt.Sprintf("%s:%d:%d: unsupported field %s is ignored", filename, pos.Line, pos.Col, JoinConfigPath(parts...))
				if legacySDKConfigPath(parts) {
					message += "; run `dagger ws migrate` to migrate it"
				}
				warnings = append(warnings, message)
				continue
			}
			if child, ok := tree.GetPath([]string{key}).(*toml.Tree); ok && childType.Kind() != reflect.Interface {
				visit(child, childType, parts)
			}
		}
	}
	visit(tree, reflect.TypeFor[Config](), nil)
	return warnings, nil
}

func legacySDKConfigPath(parts []string) bool {
	return len(parts) == 3 && configDecoderKeyMatches(parts[0], "modules") && configDecoderKeyMatches(parts[2], "as-sdk") ||
		len(parts) == 5 && configDecoderKeyMatches(parts[0], "env") && configDecoderKeyMatches(parts[2], "modules") && configDecoderKeyMatches(parts[4], "as-sdk")
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
