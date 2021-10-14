package mod

import "github.com/hashicorp/go-version"

// compareVersions returns -1 if the first argument is less or 1 if it's greater than the second argument.
// It returns 0 if they are equal.
func compareVersions(reqV1, reqV2 string) (int, error) {
	v1, err := version.NewVersion(reqV1)
	if err != nil {
		return 0, err
	}

	v2, err := version.NewVersion(reqV2)
	if err != nil {
		return 0, err
	}

	if v1.LessThan(v2) {
		return -1, nil
	}

	if v1.Equal(v2) {
		return 0, nil
	}

	return 1, nil
}
