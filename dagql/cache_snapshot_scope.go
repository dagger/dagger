package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// canonicalPath is the storage scope key, separate from a full part address.
func canonicalPath(path PersistedRefPath) (string, error) {
	for _, elem := range path {
		if elem.IsIndex {
			if elem.Index < 0 || elem.Field != "" {
				return "", fmt.Errorf("invalid path index")
			}
		} else if elem.Field == "" || elem.Index != 0 {
			return "", fmt.Errorf("invalid path field")
		}
	}
	if path == nil {
		path = PersistedRefPath{}
	}
	data, err := json.Marshal(path)
	return string(data), err
}

func cloneSnapshotRefLinks(links []PersistedSnapshotRefLink) []PersistedSnapshotRefLink {
	if links == nil {
		return nil
	}
	copy := make([]PersistedSnapshotRefLink, len(links))
	for i, link := range links {
		copy[i] = link
		copy[i].OutputPath = slices.Clone(link.OutputPath)
	}
	return copy
}

// ClonePersistedSnapshotLinks gives codec adapters the same independent copy
// used by cache capture, decode and publication.
func ClonePersistedSnapshotLinks(links []PersistedSnapshotRefLink) []PersistedSnapshotRefLink {
	return cloneSnapshotRefLinks(links)
}

func prefixSnapshotLinks(links []PersistedSnapshotRefLink, path PersistedRefPath) []PersistedSnapshotRefLink {
	links = cloneSnapshotRefLinks(links)
	for i := range links {
		links[i].OutputPath = append(slices.Clone(path), links[i].OutputPath...)
	}
	return links
}

func snapshotLinkKey(link PersistedSnapshotRefLink) (snapshotOwnerKey, error) {
	path, err := canonicalPath(link.OutputPath)
	if err != nil {
		return snapshotOwnerKey{}, err
	}
	if link.Role == "" {
		return snapshotOwnerKey{}, fmt.Errorf("empty snapshot role")
	}
	return snapshotOwnerKey{Path: path, Role: link.Role}, nil
}

func persistedEnvelopeAt(env *PersistedResultEnvelope, path PersistedRefPath) (*PersistedResultEnvelope, error) {
	if _, err := canonicalPath(path); err != nil {
		return nil, err
	}
	for len(path) > 0 {
		if env.Kind != persistedResultKindList || len(path) < 2 || path[0].IsIndex || path[0].Field != "items" || !path[1].IsIndex || path[1].Index >= len(env.Items) {
			return nil, fmt.Errorf("snapshot path does not name a declared inline envelope")
		}
		env = &env.Items[path[1].Index]
		path = path[2:]
	}
	return env, nil
}

// partitionSnapshotLinks validates exhaustive row ownership before any visitor
// runs. Projections are private; write-back can change only the storage key.
type snapshotLinkScopes struct {
	links   []PersistedSnapshotRefLink
	indices map[string][]int
}

func partitionSnapshotLinks(env PersistedResultEnvelope, links []PersistedSnapshotRefLink) (*snapshotLinkScopes, error) {
	s := &snapshotLinkScopes{links: cloneSnapshotRefLinks(links), indices: map[string][]int{}}
	seen := map[snapshotOwnerKey]bool{}
	for i, link := range s.links {
		key, err := snapshotLinkKey(link)
		if err != nil {
			return nil, err
		}
		if link.RefKey == "" {
			return nil, fmt.Errorf("snapshot role %q has empty key", link.Role)
		}
		if seen[key] {
			return nil, fmt.Errorf("storage role %q declared twice at %s", key.Role, key.Path)
		}
		seen[key] = true
		leaf, err := persistedEnvelopeAt(&env, link.OutputPath)
		if err != nil {
			return nil, err
		}
		if leaf.Kind != persistedResultKindObject {
			return nil, fmt.Errorf("snapshot role %q path ends in %s, not a codec envelope", link.Role, leaf.Kind)
		}
		if _, ok := PersistedObjectFamilyByName(leaf.ObjectCodec); !ok {
			return nil, fmt.Errorf("unknown storage codec %q", leaf.ObjectCodec)
		}
		s.indices[key.Path] = append(s.indices[key.Path], i)
	}
	return s, nil
}

func (s *snapshotLinkScopes) project(path PersistedRefPath) []PersistedSnapshotRefLink {
	key, _ := canonicalPath(path)
	var selected []PersistedSnapshotRefLink
	for _, i := range s.indices[key] {
		selected = append(selected, s.links[i])
	}
	out := cloneSnapshotRefLinks(selected)
	for i := range out {
		out[i].OutputPath = nil
	}
	return out
}

func (s *snapshotLinkScopes) rewrite(path PersistedRefPath, projection []PersistedSnapshotRefLink) error {
	key, _ := canonicalPath(path)
	indices := s.indices[key]
	if len(indices) != len(projection) {
		return fmt.Errorf("snapshot visitor changed role count")
	}
	for i, link := range projection {
		if len(link.OutputPath) != 0 || link.Role != s.links[indices[i]].Role || link.RefKey == "" {
			return fmt.Errorf("snapshot visitor changed scope or role")
		}
	}
	for i, link := range projection {
		s.links[indices[i]].RefKey = link.RefKey
	}
	return nil
}

var errOwnerLeaseReconciliation = errors.New("reconcile owner leases")

func (c *Cache) reconcileEmptyOwnerLeases(ctx context.Context) error {
	if c.snapshotManager == nil {
		return nil
	}
	if err := c.snapshotManager.DeleteStaleDaggerOwnerLeases(ctx, map[string]struct{}{}); err != nil {
		return fmt.Errorf("%w: %w", errOwnerLeaseReconciliation, err)
	}
	return nil
}
