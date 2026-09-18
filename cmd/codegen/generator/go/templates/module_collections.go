package templates

import "fmt"

// CollectionPragma is shared with the runtime build's private-state pass.
func CollectionPragma(doc string) (bool, error) {
	pragmas, _ := parsePragmaComment(doc)
	return collectionPragma(pragmas, "collection")
}

func collectionPragma(pragmas map[string]any, name string) (bool, error) {
	value, exists := pragmas[name]
	if !exists {
		return false, nil
	}
	if value == nil {
		return true, nil
	}
	enabled, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s pragma must be a boolean", name)
	}
	return enabled, nil
}
