package dagql

import "fmt"

// walkInlineValues reads only held values and declared list/nullable structure.
// Separately attached items are ownership boundaries, not inline values.
func walkInlineValues(res AnyResult, frame *ResultCall, path PersistedRefPath, root bool, visit func(AnyResult, PersistedRefPath) error) error {
	if res == nil {
		return nil
	}
	if row := res.cacheSharedResult(); !root && row != nil && row.id != 0 {
		return nil
	}
	value, present := res.DerefValue()
	if !present || value == nil || value.Unwrap() == nil {
		return nil
	}
	if list, ok := value.Unwrap().(Enumerable); ok {
		if frame == nil || frame.Type == nil || frame.Type.Elem == nil {
			return fmt.Errorf("inline list: missing authoritative list call")
		}
		for i := 1; i <= list.Len(); i++ {
			item, err := list.NthValue(i, frame)
			if err != nil {
				return err
			}
			if err := walkInlineValues(item, persistedListItemCall(frame, i), path.Field("items").Index(i-1), false, visit); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(value, path)
}

// inlineValueAt never creates an identity or crosses an attached child row.
func inlineValueAt(res AnyResult, frame *ResultCall, path PersistedRefPath) (AnyResult, error) {
	if _, err := canonicalPath(path); err != nil {
		return nil, err
	}
	for len(path) > 0 {
		value, present := res.DerefValue()
		if !present || value == nil {
			return nil, fmt.Errorf("inline path enters absent value")
		}
		list, ok := value.Unwrap().(Enumerable)
		if !ok || frame == nil || frame.Type == nil || frame.Type.Elem == nil || len(path) < 2 || path[0].IsIndex || path[0].Field != "items" || !path[1].IsIndex {
			return nil, fmt.Errorf("invalid inline item path")
		}
		i := path[1].Index
		var err error
		res, err = list.NthValue(i+1, frame)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, fmt.Errorf("inline path names null")
		}
		if row := res.cacheSharedResult(); row != nil && row.id != 0 {
			return nil, fmt.Errorf("inline path crosses result_ref")
		}
		frame = persistedListItemCall(frame, i+1)
		path = path[2:]
	}
	return res, nil
}

func snapshotOwnerLinksFromTyped(self Typed, frame *ResultCall) ([]PersistedSnapshotRefLink, error) {
	return collectSnapshotOwnerLinks(self, frame, false)
}

// snapshotOwnerLinksForSync is only called outside graph locks. The ordinary
// collector remains nonblocking for capture, boot and import.
func snapshotOwnerLinksForSync(self Typed, frame *ResultCall) ([]PersistedSnapshotRefLink, error) {
	return collectSnapshotOwnerLinks(self, frame, true)
}

func collectSnapshotOwnerLinks(self Typed, frame *ResultCall, forSync bool) ([]PersistedSnapshotRefLink, error) {
	if self == nil {
		return nil, nil
	}
	var links []PersistedSnapshotRefLink
	var versions capturedOutputVersions
	err := walkInlineValues(newDetachedResult(frame, self), frame, nil, true, func(value AnyResult, path PersistedRefPath) error {
		self := value.Unwrap()
		if reader, ok := self.(SnapshotOwnerReader); forSync && ok {
			revision, local, err := reader.ReadSnapshotOwner()
			if err != nil {
				return err
			}
			versions = append(versions, capturedOutputVersion{snapshotOwnerVersion{reader}, revision})
			links = append(links, prefixSnapshotLinks(local, path)...)
			return nil
		}
		if version, ok := self.(PersistedOutputVersion); ok {
			if err := versions.record(version); err != nil {
				return err
			}
		}
		if provider, ok := self.(interface {
			PersistedSnapshotRefLinksChecked() ([]PersistedSnapshotRefLink, error)
		}); ok {
			local, err := provider.PersistedSnapshotRefLinksChecked()
			if err != nil {
				return err
			}
			links = append(links, prefixSnapshotLinks(local, path)...)
		} else if provider, ok := self.(PersistedSnapshotRefLinkProvider); ok {
			links = append(links, prefixSnapshotLinks(provider.PersistedSnapshotRefLinks(), path)...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := versions.check(); err != nil {
		return nil, err
	}
	seen := map[snapshotOwnerKey]bool{}
	for _, link := range links {
		key, err := snapshotLinkKey(link)
		if err != nil {
			return nil, err
		}
		if link.RefKey == "" || seen[key] {
			return nil, fmt.Errorf("invalid or duplicate scoped snapshot role")
		}
		seen[key] = true
	}
	return links, nil
}
