package build

import (
	"encoding/csv"
	"strings"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/pkg/errors"
)

// ParseSecret parses --secret
func ParseSecret(sl []string) (session.Attachable, error) {
	fs := make([]secretsprovider.Source, 0, len(sl))
	for _, v := range sl {
		s, err := parseSecret(v)
		if err != nil {
			return nil, err
		}
		fs = append(fs, *s)
	}
	store, err := secretsprovider.NewStore(fs)
	if err != nil {
		return nil, err
	}
	return secretsprovider.NewSecretProvider(store), nil
}

func parseSecret(val string) (*secretsprovider.Source, error) {
	csvReader := csv.NewReader(strings.NewReader(val))
	fields, err := csvReader.Read()
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse csv secret")
	}

	fs := secretsprovider.Source{}

	var typ string
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return nil, errors.Errorf("invalid field '%s' must be a key=value pair", field)
		}
		key = strings.ToLower(key)
		switch key {
		case "type":
			if value != "file" && value != "env" {
				return nil, errors.Errorf("unsupported secret type %q", value)
			}
			typ = value
		case "id":
			fs.ID = value
		case "source", "src":
			fs.FilePath = value
		case "env":
			fs.Env = value
		default:
			return nil, errors.Errorf("unexpected key '%s' in '%s'", key, field)
		}
	}
	if typ == "env" && fs.Env == "" {
		fs.Env = fs.FilePath
		fs.FilePath = ""
	}
	return &fs, nil
}
