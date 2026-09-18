package dagql

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

func transferExtras(extras []call.ExtraDigest) []call.ExtraDigest {
	marked := map[digest.Digest]bool{}
	for _, extra := range extras {
		if extra.Label == call.ExtraDigestLabelRemoteCache {
			marked[extra.Digest] = true
		}
	}
	var kept []call.ExtraDigest
	for _, extra := range extras {
		if extra.Digest != "" && marked[extra.Digest] && (extra.Label == call.ExtraDigestLabelRemoteCache || extra.Label == call.ExtraDigestLabelContent) {
			kept = append(kept, extra)
		}
	}
	return kept
}

func walkTransferCalls(frame *ResultCall, visit func(*ResultCall) error, refVisit func(*ResultCallRef) error) error {
	seen := map[*ResultCall]bool{}
	var walk func(*ResultCall) error
	var ref func(*ResultCallRef) error
	var literal func(*ResultCallLiteral) error
	ref = func(r *ResultCallRef) error {
		if r == nil {
			return nil
		}
		if refVisit != nil {
			if err := refVisit(r); err != nil {
				return err
			}
		}
		return walk(r.Call)
	}
	literal = func(lit *ResultCallLiteral) error {
		if lit == nil {
			return nil
		}
		if err := ref(lit.ResultRef); err != nil {
			return err
		}
		for _, item := range lit.ListItems {
			if err := literal(item); err != nil {
				return err
			}
		}
		for _, arg := range lit.ObjectFields {
			if arg != nil {
				if err := literal(arg.Value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	walk = func(frame *ResultCall) error {
		if frame == nil || seen[frame] {
			return nil
		}
		seen[frame] = true
		if err := visit(frame); err != nil {
			return err
		}
		if err := ref(frame.Receiver); err != nil {
			return err
		}
		if frame.Module != nil {
			if err := ref(frame.Module.ResultRef); err != nil {
				return err
			}
		}
		for _, args := range [][]*ResultCallArg{frame.Args, frame.ImplicitInputs} {
			for _, arg := range args {
				if arg != nil {
					if err := literal(arg.Value); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return walk(frame)
}

// walkTransferPayloads visits declared envelopes, never arbitrary JSON fields.
func walkTransferPayloads(env *PersistedResultEnvelope, frame *ResultCall, links []PersistedSnapshotRefLink, path PersistedRefPath, visit func(PersistedObjectFamily, PersistedPayloadVisit) (json.RawMessage, error)) error {
	switch env.Kind {
	case persistedResultKindObject:
		family, ok := PersistedObjectFamilyByName(env.ObjectCodec)
		if !ok {
			return fmt.Errorf("unknown object codec %q at %s", env.ObjectCodec, path)
		}
		raw, err := visit(family, PersistedPayloadVisit{Version: env.Version, Call: frame, Path: path, Payload: env.ObjectJSON, SnapshotLinks: links})
		if err != nil {
			return fmt.Errorf("codec %s at %s: %w", family.Name, path, err)
		}
		env.ObjectJSON = raw
	case persistedResultKindList:
		for i := range env.Items {
			childCall := frame
			if frame != nil {
				childCall = persistedListItemCall(frame, i+1)
			}
			if err := walkTransferPayloads(&env.Items[i], childCall, nil, path.Field("items").Index(i), visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func mapTransferredOutputs(rec PersistedRecord) ([]CapturedCodecOutput, error) {
	var outputs []CapturedCodecOutput
	err := walkTransferPayloads(&rec.Envelope, rec.Call, rec.SnapshotLinks, nil, func(family PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		if family.Transfer != nil {
			mapped, err := family.Transfer.MapSnapshotParts(v)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, mapped...)
		}
		return v.Payload, nil
	})
	return outputs, err
}
func normalizeTransferRecord(rec PersistedRecord) (PersistedRecord, error) {
	var err error
	rec.Envelope, err = clonePersistedEnvelope(rec.Envelope)
	if err != nil {
		return PersistedRecord{}, err
	}
	rec.Call = rec.Call.clone()
	if err := walkTransferCalls(rec.Call, func(frame *ResultCall) error { frame.ExtraDigests = transferExtras(frame.ExtraDigests); return nil }, func(ref *ResultCallRef) error { ref.shared = nil; return nil }); err != nil {
		return PersistedRecord{}, err
	}
	if err := walkTransferPayloads(&rec.Envelope, rec.Call, rec.SnapshotLinks, nil, func(family PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		if family.Transfer == nil {
			if len(v.SnapshotLinks) != 0 {
				return nil, fmt.Errorf("storage codec has no transfer implementation")
			}
			return slices.Clone(v.Payload), nil
		}
		normalized, err := family.Transfer.NormalizeForeign(v)
		return normalized.JSON, err
	}); err != nil {
		return PersistedRecord{}, err
	}
	rec.SnapshotLinks = nil
	return VisitEncodedReferences(rec, func(ref *PersistedRef) error {
		if ref.RecipeID != nil {
			copied, err := ref.RecipeID.FilterTransferDigests()
			ref.RecipeID = copied
			return err
		}
		return nil
	})
}
func validateTransferRecord(rec PersistedRecord, deps []uint64) error {
	if rec.ResultID == 0 || rec.Envelope.ResultID != rec.ResultID {
		return fmt.Errorf("row/envelope self ID mismatch")
	}
	if rec.Call == nil || rec.Call.Type == nil || rec.Call.Type.NamedType == "Query" {
		return fmt.Errorf("missing transferable result call")
	}
	if err := validateTransferEnvelope(rec.Envelope, rec.Call.Type, true); err != nil {
		return err
	}
	if len(rec.SnapshotLinks) != 0 {
		return fmt.Errorf("bundle contains local storage links")
	}
	if err := walkTransferCalls(rec.Call, func(frame *ResultCall) error {
		if !slices.Equal(frame.ExtraDigests, transferExtras(frame.ExtraDigests)) {
			return fmt.Errorf("unmarked frame extra digest")
		}
		return nil
	}, nil); err != nil {
		return err
	}
	if err := walkTransferPayloads(&rec.Envelope, rec.Call, nil, nil, func(family PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		if family.Transfer != nil {
			if err := family.Transfer.ValidateForeign(v); err != nil {
				return nil, err
			}
		}
		return v.Payload, nil
	}); err != nil {
		return err
	}
	direct := map[uint64]bool{}
	for _, id := range deps {
		if id == 0 || direct[id] {
			return fmt.Errorf("zero or duplicate dependency %d", id)
		}
		direct[id] = true
	}
	env := rec.Envelope
	for _, offer := range env.PendingOffers {
		if err := validateOfferReferences(offer); err != nil {
			return err
		}
	}
	rec.Envelope.PendingOffers = nil
	_, err := VisitEncodedReferences(rec, func(ref *PersistedRef) error {
		if ref.RecipeID != nil {
			copy, err := ref.RecipeID.FilterTransferDigests()
			if err != nil {
				return err
			}
			before, err := ref.RecipeID.Encode()
			if err != nil {
				return err
			}
			after, err := copy.Encode()
			if err != nil {
				return err
			}
			if before != after {
				return fmt.Errorf("unmarked recipe ID extras at %s", ref.Path)
			}
			return nil
		}
		if (ref.Kind == PersistedRefChild || ref.Kind == PersistedRefCall) && !direct[ref.ResultID] {
			return fmt.Errorf("reference %s to %d is not a direct dependency", ref.Path, ref.ResultID)
		}
		return nil
	})
	return err
}

func validateTransferEnvelope(env PersistedResultEnvelope, typ *ResultCallType, root bool) error {
	if typ == nil {
		return fmt.Errorf("missing envelope type")
	}
	if !root && env.Kind != persistedResultKindRef && env.ResultID != 0 {
		return fmt.Errorf("inline envelope has self ID")
	}
	if !root && env.SessionResourceHandle != "" {
		return fmt.Errorf("inline resource handle has no owner row")
	}
	return validateTransferEnvelopeKind(env, typ, root)
}

// validateTransferEnvelopeKind is validateTransferEnvelope's per-kind check,
// one case per envelope kind.
func validateTransferEnvelopeKind(env PersistedResultEnvelope, typ *ResultCallType, root bool) error {
	switch env.Kind {
	case persistedResultKindNull:
		if typ.NonNull {
			return fmt.Errorf("null under non-null type")
		}
		if len(env.ScalarJSON) != 0 {
			return fmt.Errorf("null carries scalar payload")
		}
	case persistedResultKindRef:
		if root || env.ResultID == 0 || env.TypeName != "" || env.ObjectCodec != "" || len(env.ObjectJSON) != 0 || len(env.ScalarJSON) != 0 || len(env.Items) != 0 {
			return fmt.Errorf("invalid result reference envelope")
		}
	case persistedResultKindList:
		if typ.Elem == nil || len(env.ObjectJSON) != 0 || len(env.ScalarJSON) != 0 || env.ObjectCodec != "" {
			return fmt.Errorf("invalid list envelope")
		}
		for _, item := range env.Items {
			if err := validateTransferEnvelope(item, typ.Elem, false); err != nil {
				return err
			}
		}
	case persistedResultKindObject:
		if typ.Elem != nil || env.TypeName == "" || len(env.Items) != 0 || len(env.ScalarJSON) != 0 || !json.Valid(env.ObjectJSON) {
			return fmt.Errorf("invalid object envelope")
		}
	case persistedResultKindScalar:
		if typ.Elem != nil || env.TypeName == "" || env.ObjectCodec != "" || !json.Valid(env.ScalarJSON) {
			return fmt.Errorf("invalid scalar envelope")
		}
	default:
		return fmt.Errorf("unknown envelope kind %q", env.Kind)
	}
	return nil
}

func validateSnapshotValue(value SnapshotValue) error {
	switch value.Kind {
	case "directory", "file", "snapshot":
	default:
		return fmt.Errorf("invalid snapshot descriptor kind %q", value.Kind)
	}
	for _, service := range value.Services {
		if service.ServiceResultID == 0 {
			return fmt.Errorf("zero service reference")
		}
	}
	return nil
}

func validateTransferOffer(offer PersistedPartOffer, part CapturedCodecOutput) error {
	if err := validateOfferReferences(offer); err != nil {
		return err
	}
	if err := validateSnapshotValue(offer.Value); err != nil {
		return err
	}
	if (part.ValueKind != "" && part.ValueKind != offer.Value.Kind) || (part.Value != nil && part.Value.Kind != offer.Value.Kind) {
		return fmt.Errorf("offer kind does not match part")
	}
	if part.Address.Part == "metadata" {
		return fmt.Errorf("metadata cannot carry a chain")
	}
	blobs := map[digest.Digest]bool{}
	for _, layer := range offer.Chain.Layers {
		if err := layer.Descriptor.Digest.Validate(); err != nil {
			return fmt.Errorf("invalid layer digest: %w", err)
		}
		if layer.Descriptor.Size < 0 || layer.Descriptor.MediaType == "" {
			return fmt.Errorf("invalid layer descriptor")
		}
		blobs[layer.Descriptor.Digest] = true
	}
	for dig := range offer.Chain.Addresses {
		if !blobs[dig] {
			return fmt.Errorf("address for unknown layer %s", dig)
		}
	}
	return nil
}

// A result_ref selects the separately owned row. Inline list paths keep the
// envelope's field/index spelling and never interpret arbitrary payload JSON.
func resolveSelectedRecord(id uint64, address PersistedPartAddress, lookup func(uint64) (PersistedRecord, bool)) (uint64, PersistedPartAddress, error) {
	if _, err := partAddressKey(address); err != nil {
		return 0, address, err
	}
	record, ok := lookup(id)
	if !ok {
		return 0, address, fmt.Errorf("missing selected row %d", id)
	}
	env := record.Envelope
	path := address.OutputPath
	consumed := 0
	seen := map[uint64]bool{id: true}
	for {
		if env.Kind == persistedResultKindRef {
			id = env.ResultID
			if seen[id] {
				return 0, address, fmt.Errorf("cycle in selected value")
			}
			seen[id] = true
			record, ok = lookup(id)
			if !ok {
				return 0, address, fmt.Errorf("missing selected row %d", id)
			}
			env = record.Envelope
			address.OutputPath = slices.Clone(path[consumed:])
			path, consumed = address.OutputPath, 0
			continue
		}
		if consumed == len(path) {
			return id, address, nil
		}
		if env.Kind != persistedResultKindList || len(path)-consumed < 2 || path[consumed].IsIndex || path[consumed].Field != "items" || !path[consumed+1].IsIndex {
			return 0, address, fmt.Errorf("invalid selected output path %s", path)
		}
		index := path[consumed+1].Index
		if index < 0 || index >= len(env.Items) {
			return 0, address, fmt.Errorf("selected item index out of bounds")
		}
		env = env.Items[index]
		consumed += 2
	}
}
