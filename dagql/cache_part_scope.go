package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// partRecordAt rebases only the codec view; the row and its ownership stay put.
func partRecordAt(record PersistedRecord, path PersistedRefPath) (PersistedRecord, error) {
	scopes, err := partitionSnapshotLinks(record.Envelope, record.SnapshotLinks)
	if err != nil {
		return PersistedRecord{}, err
	}
	leaf, err := persistedEnvelopeAt(&record.Envelope, path)
	if err != nil {
		return PersistedRecord{}, err
	}
	if leaf.Kind != persistedResultKindObject {
		return PersistedRecord{}, fmt.Errorf("part scope is not a codec envelope")
	}
	frame := record.Call
	for i := 1; i < len(path); i += 2 {
		if frame != nil {
			frame = persistedListItemCall(frame, path[i].Index+1)
		}
	}
	return PersistedRecord{ResultID: record.ResultID, Envelope: *leaf, Call: frame, SnapshotLinks: scopes.project(path)}, nil
}

func replacePartRecord(record PersistedRecord, path PersistedRefPath, replacement PersistedRecord) (PersistedRecord, error) {
	next, err := clonePersistedEnvelope(record.Envelope)
	if err != nil {
		return PersistedRecord{}, err
	}
	leaf, err := persistedEnvelopeAt(&next, path)
	if err != nil {
		return PersistedRecord{}, err
	}
	*leaf = replacement.Envelope
	links := cloneSnapshotRefLinks(record.SnapshotLinks)
	links = slices.DeleteFunc(links, func(link PersistedSnapshotRefLink) bool { return slices.Equal(link.OutputPath, path) })
	links = append(links, prefixSnapshotLinks(replacement.SnapshotLinks, path)...)
	record.Envelope, record.SnapshotLinks = next, links
	return record, nil
}

func prepareScopedPartRecord(record, source PersistedRecord, descriptor PartDescriptor, address PersistedPartAddress) (PersistedRecord, error) {
	current, err := partRecordAt(record, address.OutputPath)
	if err != nil {
		return PersistedRecord{}, err
	}
	donor, err := partRecordAt(source, descriptor.Address.OutputPath)
	if err != nil {
		return PersistedRecord{}, err
	}
	family, ok := PersistedObjectFamilyByName(current.Envelope.ObjectCodec)
	if !ok {
		return PersistedRecord{}, fmt.Errorf("unknown part codec")
	}
	installer, ok := family.Transfer.(PersistedPartInstaller)
	if !ok {
		return PersistedRecord{}, fmt.Errorf("codec has no part installer")
	}
	descriptor.Address.OutputPath = nil
	next, err := installer.PreparePartRecord(current, donor, descriptor, PersistedPartAddress{Part: address.Part})
	if err != nil {
		return PersistedRecord{}, err
	}
	return replacePartRecord(record, address.OutputPath, next)
}

func (dec *PersistDecodeContext) atPath(path PersistedRefPath) *PersistDecodeContext {
	for i := 1; i < len(path); i += 2 {
		var frame *ResultCall
		if dec.call != nil {
			frame = persistedListItemCall(dec.call, path[i].Index+1)
		}
		dec = dec.item(frame, path[i].Index)
	}
	return dec
}

func describePartRecord(record PersistedRecord) ([]PartProbe, error) {
	var probes []PartProbe
	err := walkTransferPayloads(&record.Envelope, record.Call, record.SnapshotLinks, nil, func(family PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		if describer, ok := family.Transfer.(PersistedPartDescriber); ok {
			parts, err := describer.DescribeParts(v)
			if err != nil {
				return nil, err
			}
			for i := range parts {
				parts[i].Descriptor.Family = family.Name
			}
			probes = append(probes, parts...)
		}
		return v.Payload, nil
	})
	return probes, err
}

func (c *Cache) openAcquiredPart(ctx context.Context, res AnyResult, address PersistedPartAddress) error {
	value, err := inlineValueAt(res, res.cacheSharedResult().loadResultCall(), address.OutputPath)
	if err != nil {
		return err
	}
	if opener, ok := UnwrapAs[PartOutputOpener](value); ok {
		return opener.OpenPart(ctx, PersistedPartAddress{Part: address.Part})
	}
	return nil
}
