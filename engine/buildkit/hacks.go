package buildkit

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

func EncodeIDHack(val any) (string, error) {
	hack, err := json.Marshal(val)
	if err != nil {
		return "", err
	}

	return base64.URLEncoding.EncodeToString([]byte(hack)), nil
}

func DecodeIDHack(scheme string, id string, val any) error {
	// NB: convenience; sometimes it won't be there (e.g. local dirs), sometimes
	// it will (e.g. http)
	id = strings.TrimPrefix(id, scheme+"://")

	jsonBytes, err := base64.URLEncoding.DecodeString(id)
	if err != nil {
		return err
	}

	return json.Unmarshal(jsonBytes, &val)
}
