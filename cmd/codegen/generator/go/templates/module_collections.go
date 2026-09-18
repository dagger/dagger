package templates

import "fmt"

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
