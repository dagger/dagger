package build

import (
	"encoding/csv"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/containerd/console"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session/filesync"
	"github.com/pkg/errors"
)

// parseOutputCSV parses a single --output CSV string
func parseOutputCSV(s string) (client.ExportEntry, error) {
	ex := client.ExportEntry{
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
		return ex, errors.New("--output requires type=<type>")
	}
	if v, ok := ex.Attrs["output"]; ok {
		return ex, errors.Errorf("output=%s not supported for --output, you meant dest=%s?", v, v)
	}
	ex.Output, ex.OutputDir, err = resolveExporterDest(ex.Type, ex.Attrs["dest"], ex.Attrs)
	if err != nil {
		return ex, errors.Wrap(err, "invalid output option: output")
	}
	if ex.Output != nil || ex.OutputDir != "" {
		delete(ex.Attrs, "dest")
	}
	return ex, nil
}

// ParseOutput parses --output
func ParseOutput(exports []string) ([]client.ExportEntry, error) {
	var entries []client.ExportEntry
	for _, s := range exports {
		e, err := parseOutputCSV(s)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// resolveExporterDest returns at most either one of io.WriteCloser (single file) or a string (directory path).
func resolveExporterDest(exporter, dest string, attrs map[string]string) (filesync.FileOutputFunc, string, error) {
	wrapWriter := func(wc io.WriteCloser) func(map[string]string) (io.WriteCloser, error) {
		return func(m map[string]string) (io.WriteCloser, error) {
			return wc, nil
		}
	}

	var supportFile bool
	var supportDir bool
	switch exporter {
	case client.ExporterLocal:
		supportDir = true
	case client.ExporterTar:
		supportFile = true
	case client.ExporterOCI, client.ExporterDocker:
		tar, err := strconv.ParseBool(attrs["tar"])
		if err != nil {
			tar = true
		}
		supportFile = tar
		supportDir = !tar
	}

	if supportDir {
		if dest == "" {
			return nil, "", errors.Errorf("output directory is required for %s exporter", exporter)
		}
		return nil, dest, nil
	} else if supportFile {
		if dest != "" && dest != "-" {
			fi, err := os.Stat(dest)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, "", errors.Wrapf(err, "invalid destination file: %s", dest)
			}
			if err == nil && fi.IsDir() {
				return nil, "", errors.Errorf("destination file is a directory")
			}
			w, err := os.Create(dest)
			return wrapWriter(w), "", err
		}
		// if no output file is specified, use stdout
		if _, err := console.ConsoleFromFile(os.Stdout); err == nil {
			return nil, "", errors.Errorf("output file is required for %s exporter. refusing to write to console", exporter)
		}
		return wrapWriter(os.Stdout), "", nil
	}
	// e.g. client.ExporterImage
	if dest != "" {
		return nil, "", errors.Errorf("output %s is not supported by %s exporter", dest, exporter)
	}
	return nil, "", nil
}
