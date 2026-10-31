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
	err := walkTransferPayloads(&record.Envelope, record.Call, record.SnapshotLinks, func(family PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
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

// partDescription collects the complete parts of a row described rather than
// encoded: those describePartRecord reports for the record it would encode
// to. Each object is described as that record's payload walk describes it,
// except that an object that describes its own parts isn't encoded.
type partDescription struct {
	complete []PersistedPartAddress
}

func (d *partDescription) describe(ctx context.Context, enc *PersistEncodeContext, family PersistedObjectFamily, object PersistedObject) error {
	var desc PersistedPartsDescription
	described := false
	if describer, ok := object.(PersistedPartsDescriber); ok {
		var err error
		desc, described, err = describer.DescribePersistedParts(ctx, enc)
		if err != nil {
			return err
		}
	}
	if !described {
		encoding, err := object.EncodePersistedObject(ctx, enc)
		if err != nil {
			return err
		}
		desc.SnapshotLinks = encoding.SnapshotLinks
		if describer, ok := family.Transfer.(PersistedPartDescriber); ok {
			v := enc.PayloadVisit(cloneSnapshotRefLinks(encoding.SnapshotLinks))
			v.Payload = encoding.JSON
			probes, err := describer.DescribeParts(v)
			if err != nil {
				return err
			}
			desc.Complete = completePartAddresses(probes)
		}
	}
	if err := checkObjectSnapshotLinks(desc.SnapshotLinks); err != nil {
		return err
	}
	d.complete = append(d.complete, desc.Complete...)
	return nil
}

// checkObjectSnapshotLinks checks one object's snapshot links as
// partitionSnapshotLinks checks those of the record they would be part of:
// each names a role of the object itself, once, with a snapshot key.
func checkObjectSnapshotLinks(links []PersistedSnapshotRefLink) error {
	for i, link := range links {
		switch {
		case len(link.OutputPath) != 0:
			return fmt.Errorf("snapshot role %q path does not name a declared inline envelope", link.Role)
		case link.Role == "":
			return fmt.Errorf("empty snapshot role")
		case link.RefKey == "":
			return fmt.Errorf("snapshot role %q has empty key", link.Role)
		}
		for _, prior := range links[:i] {
			if prior.Role == link.Role {
				return fmt.Errorf("storage role %q declared twice", link.Role)
			}
		}
	}
	return nil
}

// completePartAddresses returns the addresses of probes' locally complete
// parts.
func completePartAddresses(probes []PartProbe) []PersistedPartAddress {
	var complete []PersistedPartAddress
	for _, probe := range probes {
		if probe.LocalComplete {
			complete = append(complete, probe.Descriptor.Address)
		}
	}
	return complete
}

// partAddressKeys returns the keys of addresses, sorted and without
// duplicates. An address without a key is left out.
func partAddressKeys(addresses []PersistedPartAddress) []string {
	var keys []string
	for _, address := range addresses {
		if key, err := partAddressKey(address); err == nil {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
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
