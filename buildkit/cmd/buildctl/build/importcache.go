package build

import (
	"encoding/csv"
	"strings"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
)

func parseImportCacheCSV(s string) (client.CacheOptionsEntry, error) {
	im := client.CacheOptionsEntry{
		Type:  "",
		Attrs: map[string]string{},
	}
	csvReader := csv.NewReader(strings.NewReader(s))
	fields, err := csvReader.Read()
	if err != nil {
		return im, err
	}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return im, errors.Errorf("invalid value %s", field)
		}
		key = strings.ToLower(key)
		switch key {
		case "type":
			im.Type = value
		default:
			im.Attrs[key] = value
		}
	}
	if im.Type == "" {
		return im, errors.New("--import-cache requires type=<type>")
	}
	if im.Type == "gha" {
		return loadGithubEnv(im)
	}
	return im, nil
}

// ParseImportCache parses --import-cache
func ParseImportCache(importCaches []string) ([]client.CacheOptionsEntry, error) {
	var imports []client.CacheOptionsEntry
	for _, importCache := range importCaches {
		legacy := !strings.Contains(importCache, "type=")
		if legacy {
			// Deprecated since BuildKit v0.4.0, but no plan to remove: https://github.com/moby/buildkit/pull/2783#issuecomment-1093449772
			bklog.L.Warn("--import-cache <ref> is deprecated. Please use --import-cache type=registry,ref=<ref>,<opt>=<optval>[,<opt>=<optval>] instead.")
			imports = append(imports, client.CacheOptionsEntry{
				Type:  "registry",
				Attrs: map[string]string{"ref": importCache},
			})
		} else {
			im, err := parseImportCacheCSV(importCache)
			if err != nil {
				return nil, err
			}
			imports = append(imports, im)
		}
	}
	return imports, nil
}
