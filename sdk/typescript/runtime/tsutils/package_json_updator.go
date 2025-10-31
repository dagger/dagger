package tsutils

import (
	"fmt"
	"typescript-sdk/tsdistconsts"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func UpdatePackageJSON(packageJSON string) (string, error) {
	packageJSON = removeJSONComments(packageJSON)

	// Set type to module
	packageJSON, err := sjson.Set(packageJSON, "type", "module")
	if err != nil {
		return "", fmt.Errorf("failed to set type to module: %w", err)
	}

	// Set typescript dependency if it's not already set
	packageJSON, err = setIfNotExists(packageJSON, "dependencies.typescript", tsdistconsts.DefaultTypeScriptVersion)
	if err != nil {
		return "", fmt.Errorf("failed to set typescript dependency: %w", err)
	}

	// Remote "@dagger.io/dagger" dependency if it's set
	// so smoothly transition from local to module
	packageJSON, err = sjson.Delete(packageJSON, "dependencies."+gjson.Escape(daggerLibPathAlias))
	if err != nil {
		return "", fmt.Errorf("failed to delete @dagger.io/dagger dependency: %w", err)
	}

	return packageJSON, nil
}
