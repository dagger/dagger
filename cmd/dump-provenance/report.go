package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dagger/dagger/core/provenance"
)

type collection struct {
	Mode         string `json:"mode"`
	Snapshot     bool   `json:"snapshot"`
	Workspace    string `json:"workspace,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	ArtifactType string `json:"artifactType,omitempty"`
	URIFilter    string `json:"uriFilter,omitempty"`
	Source       string `json:"source,omitempty"`
}

type selector struct {
	Path         string   `json:"path"`
	Include      []string `json:"include"`
	Exclude      []string `json:"exclude"`
	Conservative bool     `json:"conservative"`
	Kind         string   `json:"kind"`
	Origin       string   `json:"origin"`
}

type artifact struct {
	Path       []string `json:"path"`
	URI        string   `json:"uri"`
	Provenance *struct {
		Selectors []selector `json:"selectors"`
		Issues    []string   `json:"issues"`
	} `json:"provenance"`
	Matches []match `json:"matches,omitempty"`
}

type match struct {
	Mode     string `json:"mode"`
	Path     string `json:"path"`
	Affected bool   `json:"affected"`
}

type report struct {
	Collection collection `json:"collection"`
	Artifacts  []artifact `json:"artifacts"`
}

func requireFields(data []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range names {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("missing or null %s", name)
		}
	}
	return nil
}

func (s *selector) UnmarshalJSON(data []byte) error {
	if err := requireFields(data, "path", "include", "exclude", "conservative", "kind", "origin"); err != nil {
		return err
	}
	type plain selector
	return json.Unmarshal(data, (*plain)(s))
}

func (c *collection) UnmarshalJSON(data []byte) error {
	if err := requireFields(data, "mode", "snapshot"); err != nil {
		return err
	}
	type plain collection
	return json.Unmarshal(data, (*plain)(c))
}

func readReport(reader io.Reader) (*report, error) {
	var result report
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("read saved report: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("saved report must contain one JSON document")
	}
	if err := result.validate(); err != nil {
		return nil, fmt.Errorf("saved report: %w", err)
	}
	return &result, nil
}

func (r *report) validate() error {
	if r.Collection.Mode != "engine-backed" {
		return fmt.Errorf("collection.mode must be engine-backed")
	}
	if r.Artifacts == nil {
		return fmt.Errorf("missing artifacts array")
	}
	for i, a := range r.Artifacts {
		if a.Path == nil || a.URI == "" || a.Provenance == nil || a.Provenance.Selectors == nil || a.Provenance.Issues == nil {
			return fmt.Errorf("artifact %d has incomplete path, URI, or provenance", i)
		}
		for j, s := range a.Provenance.Selectors {
			if s.Path == "" || s.Include == nil || s.Exclude == nil {
				return fmt.Errorf("artifact %d selector %d is incomplete", i, j)
			}
			if _, err := provenance.Path(s.Path); err != nil {
				return fmt.Errorf("artifact %d selector %d: %w", i, j, err)
			}
			if err := provenance.ValidateKinds([]string{s.Kind}); err != nil {
				return fmt.Errorf("artifact %d selector %d: %w", i, j, err)
			}
		}
	}
	return nil
}

func (r *report) match(files, paths []string) error {
	queries := []struct {
		mode   string
		paths  []string
		method func([]provenance.Selector, string, []string) (bool, error)
	}{{"local-file", files, provenance.AffectedByFile}, {"local-subtree", paths, provenance.AffectedByPath}}
	for _, query := range queries {
		for _, path := range query.paths {
			if _, err := provenance.Path(path); err != nil {
				return fmt.Errorf("match %q: %w", path, err)
			}
		}
	}
	for i := range r.Artifacts {
		a := &r.Artifacts[i]
		a.Matches = nil
		selectors := make([]provenance.Selector, len(a.Provenance.Selectors))
		for j, s := range a.Provenance.Selectors {
			selectors[j] = provenance.Selector(s)
		}
		for _, query := range queries {
			for _, path := range query.paths {
				affected, err := query.method(selectors, path, nil)
				if err != nil {
					return fmt.Errorf("match %q for %s: %w", path, a.URI, err)
				}
				a.Matches = append(a.Matches, match{Mode: query.mode, Path: path, Affected: affected})
			}
		}
	}
	return nil
}
