package build

import (
	"encoding/csv"
	"strings"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
)

func parseExportCacheCSV(s string) (client.CacheOptionsEntry, error) {
	ex := client.CacheOptionsEntry{
		Type:  "",
		Attrs: map[string]string{},
	}
	csvReader := csv.NewReader(strings.NewReader(s))
	fields, err := csvReader.Read()
	if err != nil {
		return ex, err
	}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return ex, errors.Errorf("invalid value %s", field)
		}
		key = strings.ToLower(key)
		switch key {
		case "type":
			ex.Type = value
		default:
			ex.Attrs[key] = value
		}
	}
	if ex.Type == "" {
		return ex, errors.New("--export-cache requires type=<type>")
	}
	if _, ok := ex.Attrs["mode"]; !ok {
		ex.Attrs["mode"] = "min"
	}
	if ex.Type == "gha" {
		return loadGithubEnv(ex)
	}
	return ex, nil
}

// ParseExportCache parses --export-cache
func ParseExportCache(exportCaches []string) ([]client.CacheOptionsEntry, error) {
	var exports []client.CacheOptionsEntry
	for _, exportCache := range exportCaches {
		legacy := !strings.Contains(exportCache, "type=")
		if legacy {
			// Deprecated since BuildKit v0.4.0, but no plan to remove: https://github.com/moby/buildkit/pull/2783#issuecomment-1093449772
			bklog.L.Warnf("--export-cache <ref> is deprecated. Please use --export-cache type=registry,ref=<ref>,<opt>=<optval>[,<opt>=<optval>] instead")
			exports = append(exports, client.CacheOptionsEntry{
				Type: "registry",
				Attrs: map[string]string{
					"mode": "min",
					"ref":  exportCache,
				},
			})
		} else {
			ex, err := parseExportCacheCSV(exportCache)
			if err != nil {
				return nil, err
			}
			exports = append(exports, ex)
		}
	}
	return exports, nil
}
