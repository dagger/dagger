// Package dagaddress parses and prints DAG addresses: URLs that select
// workspace artifacts.
//
//	[dag[+<type>[+<type>...]]://][<workspace>@<version>:][<path>][?<dimension>=<key>&...]
//
// The scheme marks a workspace artifact address. Without it, the value is a
// relative address. In a relative address, ":" is accepted as a path
// separator for compatibility. In an absolute address, the first ":" after
// "@" ends the version, and later colons in the path are the old separator.
// Neither separator splits a key: the query is split off first.
package dagaddress

import (
	"fmt"
	"net/url"
	"strings"
)

// Scheme is the URL scheme of a DAG address.
const Scheme = "dag"

// Pair is one dimension selector from the query. A pair without a key
// selects any key in the dimension.
type Pair struct {
	Dimension string
	Key       string
	HasKey    bool
}

// Address is a parsed DAG address.
type Address struct {
	// HasScheme reports whether the value started with "dag://" or "dag+type://".
	HasScheme bool
	// Types lists the "+<type>" segments of the scheme, in CLI case.
	Types []string
	// Absolute reports whether the value carried "<workspace>@<version>:".
	Absolute bool
	// Workspace is the Git address of the workspace, written as a Go import path.
	Workspace string
	// Version is the branch, tag, or commit of an absolute address.
	Version string
	// Path is the field path with "/" separators. It may be a pattern. Empty
	// selects all artifacts.
	Path string
	// Query lists the dimension selectors in input order.
	Query []Pair
}

// IsAddress reports whether value starts with the DAG scheme. It does not
// validate the rest of the value.
func IsAddress(value string) bool {
	scheme, _, ok := strings.Cut(value, "://")
	if !ok {
		return false
	}
	return scheme == Scheme || strings.HasPrefix(scheme, Scheme+"+")
}

// Parse parses value as a DAG address. A value with another URL scheme is an
// error: it is an external reference, not an artifact address.
func Parse(value string) (*Address, error) {
	addr := &Address{}
	rest := value
	if scheme, after, ok := strings.Cut(value, "://"); ok {
		if scheme != Scheme && !strings.HasPrefix(scheme, Scheme+"+") {
			return nil, fmt.Errorf("not a DAG address: %q", value)
		}
		addr.HasScheme = true
		for _, typ := range strings.Split(scheme, "+")[1:] {
			if typ == "" {
				return nil, fmt.Errorf("invalid DAG address %q: empty type in scheme", value)
			}
			addr.Types = append(addr.Types, typ)
		}
		rest = after
	}

	var query string
	var hasQuery bool
	rest, query, hasQuery = strings.Cut(rest, "?")
	if hasQuery {
		pairs, err := parseQuery(query)
		if err != nil {
			return nil, fmt.Errorf("invalid DAG address %q: %w", value, err)
		}
		addr.Query = pairs
	}

	if workspace, after, ok := strings.Cut(rest, "@"); ok {
		version, path, ok := strings.Cut(after, ":")
		if !ok {
			return nil, fmt.Errorf("invalid DAG address %q: expected \"<workspace>@<version>:<path>\"; tree addresses are not supported here", value)
		}
		if workspace == "" || version == "" {
			return nil, fmt.Errorf("invalid DAG address %q: empty workspace or version", value)
		}
		addr.Absolute = true
		addr.Workspace = workspace
		addr.Version = version
		rest = path
	}
	addr.Path = strings.ReplaceAll(rest, ":", "/")
	return addr, nil
}

func parseQuery(query string) ([]Pair, error) {
	if query == "" {
		return nil, nil
	}
	var pairs []Pair
	for _, raw := range strings.Split(query, "&") {
		if raw == "" {
			continue
		}
		name, key, hasKey := strings.Cut(raw, "=")
		dimension, err := url.PathUnescape(name)
		if err != nil {
			return nil, fmt.Errorf("dimension %q: %w", name, err)
		}
		if dimension == "" {
			return nil, fmt.Errorf("empty dimension in query %q", query)
		}
		pair := Pair{Dimension: dimension, HasKey: hasKey}
		if hasKey {
			pair.Key, err = url.PathUnescape(key)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
		}
		pairs = append(pairs, pair)
	}
	return pairs, nil
}

// DimensionFilter groups the query by dimension: repeated dimensions are
// alternatives, and different dimensions combine with AND. Nil keys select
// any key in the dimension.
type DimensionFilter struct {
	Dimension string
	Keys      []string
}

// DimensionFilters groups the query pairs by dimension, in first-seen order.
// A pair without a key makes the whole dimension match any key.
func (addr *Address) DimensionFilters() []DimensionFilter {
	var filters []DimensionFilter
	index := map[string]int{}
	for _, pair := range addr.Query {
		i, ok := index[pair.Dimension]
		if !ok {
			i = len(filters)
			index[pair.Dimension] = i
			filters = append(filters, DimensionFilter{Dimension: pair.Dimension, Keys: []string{}})
		}
		if !pair.HasKey {
			filters[i].Keys = nil
		} else if filters[i].Keys != nil {
			filters[i].Keys = append(filters[i].Keys, pair.Key)
		}
	}
	return filters
}

// String prints the address with the scheme, in canonical form.
func (addr *Address) String() string {
	var b strings.Builder
	b.WriteString(Scheme)
	for _, typ := range addr.Types {
		b.WriteString("+")
		b.WriteString(typ)
	}
	b.WriteString("://")
	if addr.Absolute {
		b.WriteString(addr.Workspace)
		b.WriteString("@")
		b.WriteString(addr.Version)
		b.WriteString(":")
	}
	b.WriteString(addr.Path)
	for i, pair := range addr.Query {
		if i == 0 {
			b.WriteString("?")
		} else {
			b.WriteString("&")
		}
		b.WriteString(escape(pair.Dimension))
		if pair.HasKey {
			b.WriteString("=")
			b.WriteString(escape(pair.Key))
		}
	}
	return b.String()
}

// escape percent-encodes the characters that would split or end a query
// pair. "/" needs no escape.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '&', '=', '#', '+', '%', ' ', '?':
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
