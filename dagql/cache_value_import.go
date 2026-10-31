package dagql

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"
)

// validateValueBundle checks a bundle and every record's payload, and returns
// its ordinals in dependency order.
func validateValueBundle(bundle ValueBundle) ([]uint64, error) {
	return checkValueBundle(bundle, false)
}

// validateValueBundleShape checks a bundle as validateValueBundle does, except
// each record's payload: records are checked by validateTransferRecordShape,
// and a payload is decoded only to find the parts the record's offers and
// outputs name. The caller checks the payloads it uses.
func validateValueBundleShape(bundle ValueBundle) ([]uint64, error) {
	return checkValueBundle(bundle, true)
}

// ResolveChainPrefixes gives each of the bundle's outputs that names a chain
// prefix its chain, in place: the first layers of the chain of the output it
// names, which the two share. It refuses a prefix that names no output of the
// bundle carrying a chain, takes more layers than that chain has or none, or
// sits on an output that carries a chain too.
func ResolveChainPrefixes(bundle *ValueBundle) error {
	chains := map[string]*OfferedChain{}
	for _, output := range bundle.Outputs {
		if output.Chain == nil {
			continue
		}
		key, err := partAddressKey(output.Address)
		if err != nil {
			return err
		}
		chains[fmt.Sprintf("%d:%s", output.Ordinal, key)] = output.Chain
	}
	for i := range bundle.Outputs {
		output := &bundle.Outputs[i]
		prefix := output.Prefix
		if prefix == nil {
			continue
		}
		if output.Chain != nil {
			return fmt.Errorf("output %d has both a chain and a prefix", output.Ordinal)
		}
		key, err := partAddressKey(prefix.Address)
		if err != nil {
			return err
		}
		chain := chains[fmt.Sprintf("%d:%s", prefix.Ordinal, key)]
		if chain == nil {
			return fmt.Errorf("output %d's prefix names no output %d:%s with a chain", output.Ordinal, prefix.Ordinal, key)
		}
		if prefix.Layers < 1 || prefix.Layers > len(chain.Layers) {
			return fmt.Errorf("output %d's prefix takes %d of a chain of %d layers", output.Ordinal, prefix.Layers, len(chain.Layers))
		}
		// The full slice expression caps the prefix's layers at its own, so
		// an append to them can't write over the chain's.
		output.Chain = &OfferedChain{Layers: chain.Layers[:prefix.Layers:prefix.Layers]}
		output.Prefix = nil
	}
	return nil
}

//nolint:gocyclo // one check per bundle field and record kind; splitting hides the order of the checks
func checkValueBundle(bundle ValueBundle, shapeOnly bool) ([]uint64, error) {
	if bundle.Version != valueBundleVersion {
		return nil, fmt.Errorf("unsupported value bundle version %d", bundle.Version)
	}
	// A prefix output is checked as the chain it names: resolved on a copy of
	// the outputs, leaving the caller's bundle as it was.
	bundle.Outputs = slices.Clone(bundle.Outputs)
	if err := ResolveChainPrefixes(&bundle); err != nil {
		return nil, err
	}
	if len(bundle.Roots) == 0 {
		return nil, fmt.Errorf("bundle has no roots")
	}
	rows := map[uint64]TransferredValue{}
	for _, row := range bundle.Values {
		id := uint64(row.Ordinal)
		if id == 0 || id > uint64(len(bundle.Values)) || row.Record.ResultID != id {
			return nil, fmt.Errorf("invalid ordinal %d", id)
		}
		if _, exists := rows[id]; exists {
			return nil, fmt.Errorf("duplicate ordinal %d", id)
		}
		if row.SenderNumber == 0 {
			return nil, fmt.Errorf("row %d: no sender number", id)
		}
		if row.Held != "" {
			if err := validateHeldRow(row); err != nil {
				return nil, fmt.Errorf("row %d: %w", id, err)
			}
			rows[id] = row
			continue
		}
		validate := validateTransferRecord
		if shapeOnly {
			validate = validateTransferRecordShape
		}
		if err := validate(row.Record, row.DependencyIDs); err != nil {
			return nil, fmt.Errorf("row %d: %w", id, err)
		}
		rows[id] = row
	}
	for i, transferred := range bundle.Calls {
		if transferred.Call == "" {
			return nil, fmt.Errorf("call %d: no call", i)
		}
		if _, ok := rows[uint64(transferred.Value)]; !ok {
			return nil, fmt.Errorf("call %d: value %d is no row", i, transferred.Value)
		}
	}
	named := map[uint64]bool{}
	for _, output := range bundle.Outputs {
		named[uint64(output.Ordinal)] = true
	}
	// Validate complete addresses against the encoded output mapping. A pending
	// slot may replace the descriptor, but cannot change the part's existence.
	// Checking the shape only, a payload is mapped only for a row an offer or
	// an output names.
	addresses := map[uint64]map[string]CapturedCodecOutput{}
	for id, row := range rows {
		if row.Held != "" || shapeOnly && !named[id] && len(row.Record.Envelope.PendingOffers) == 0 {
			continue
		}
		outputs, err := mapTransferredOutputs(row.Record)
		if err != nil {
			return nil, err
		}
		addresses[id] = map[string]CapturedCodecOutput{}
		for _, output := range outputs {
			key, err := partAddressKey(output.Address)
			if err != nil {
				return nil, err
			}
			addresses[id][key] = output
		}
	}
	slots := map[string]bool{}
	validateSlot := func(id uint64, address PersistedPartAddress) error {
		key, err := partAddressKey(address)
		if err != nil {
			return err
		}
		part, ok := addresses[id][key]
		if !ok {
			return fmt.Errorf("row %d has no output at %s", id, key)
		}
		if part.State != "pending" {
			return fmt.Errorf("offer at final output %d:%s", id, key)
		}
		key = fmt.Sprintf("%d:%s", id, key)
		if slots[key] {
			return fmt.Errorf("duplicate offered address %s", key)
		}
		slots[key] = true
		return nil
	}
	for id, row := range rows {
		for _, offer := range row.Record.Envelope.PendingOffers {
			if err := validateSlot(id, offer.Address); err != nil {
				return nil, err
			}
			key, _ := partAddressKey(offer.Address)
			if err := validateTransferOffer(offer, addresses[id][key]); err != nil {
				return nil, err
			}
		}
	}
	outputDeps := map[uint64][]uint64{}
	described := map[string]bool{}
	for _, output := range bundle.Outputs {
		id := uint64(output.Ordinal)
		if row, ok := rows[id]; !ok {
			return nil, fmt.Errorf("output names missing ordinal %d", id)
		} else if row.Held != "" {
			return nil, fmt.Errorf("output names held row %d", id)
		}
		key, err := partAddressKey(output.Address)
		if err != nil {
			return nil, err
		}
		composite := fmt.Sprintf("%d:%s", id, key)
		if described[composite] {
			return nil, fmt.Errorf("duplicate output %s", composite)
		}
		described[composite] = true
		part, ok := addresses[id][key]
		if !ok {
			return nil, fmt.Errorf("unknown output address %s", composite)
		}
		switch output.State {
		case "pending", "completed", "absent", "metadata":
		default:
			return nil, fmt.Errorf("invalid output state %q", output.State)
		}
		if slots[composite] {
			return nil, fmt.Errorf("duplicate offer description %s", composite)
		}
		if output.State != part.State && (part.State != "pending" || output.State != "completed") {
			return nil, fmt.Errorf("output state differs from recorded part %s", composite)
		}
		if output.Value != nil {
			if err := validateSnapshotValue(*output.Value); err != nil {
				return nil, err
			}
			for _, service := range output.Value.Services {
				if _, ok := rows[service.ServiceResultID]; !ok {
					return nil, fmt.Errorf("output refers to missing service %d", service.ServiceResultID)
				}
			}
		}
		if output.State == "completed" && output.Chain == nil {
			return nil, fmt.Errorf("completed selection requires chain")
		}
		if output.Chain == nil {
			if output.Owner != nil {
				return nil, fmt.Errorf("output owner without chain")
			}
			continue
		}
		if output.Value == nil || output.Owner == nil || output.State != "completed" {
			return nil, fmt.Errorf("chain requires completed descriptor and owner")
		}
		offer := PersistedPartOffer{Address: output.Address, Value: *output.Value, Chain: *output.Chain, Owner: *output.Owner}
		if err := validateTransferOffer(offer, part); err != nil {
			return nil, err
		}
		if err := validateSlot(id, output.Address); err != nil {
			return nil, err
		}
		outputDeps[id] = append(outputDeps[id], output.Owner.DependencyIDs...)
	}
	seen := map[uint64]uint8{}
	var order []uint64
	var walk func(uint64) error
	walk = func(id uint64) error {
		row, ok := rows[id]
		if !ok {
			return fmt.Errorf("missing dependency ordinal %d", id)
		}
		if seen[id] == 1 {
			return fmt.Errorf("ownership cycle through ordinal %d", id)
		}
		if seen[id] == 2 {
			return nil
		}
		seen[id] = 1
		deps := slices.Clone(row.DependencyIDs)
		for _, offer := range row.Record.Envelope.PendingOffers {
			deps = append(deps, offer.Owner.DependencyIDs...)
		}
		deps = append(deps, outputDeps[id]...)
		for _, id := range deps {
			if err := walk(id); err != nil {
				return err
			}
		}
		seen[id] = 2
		order = append(order, id)
		return nil
	}
	roots := map[uint64]bool{}
	for _, root := range bundle.Roots {
		id := uint64(root.Ordinal)
		if roots[id] {
			return nil, fmt.Errorf("duplicate root %d", id)
		}
		roots[id] = true
		if err := walk(id); err != nil {
			return nil, err
		}
	}
	if len(seen) != len(rows) {
		return nil, fmt.Errorf("bundle contains rows outside its root closure")
	}
	return order, nil
}

// validateHeldRow checks a held row: a recipe digest, in its algorithm:encoded
// form (recipes are xxh3, which go-digest doesn't validate), and nothing a
// record would carry but its ordinal.
func validateHeldRow(row TransferredValue) error {
	if algorithm, encoded, ok := strings.Cut(string(row.Held), ":"); !ok || algorithm == "" || encoded == "" {
		return fmt.Errorf("held recipe %q is not a digest", row.Held)
	}
	if len(row.DependencyIDs) > 0 || row.Record.Call != nil || len(row.Record.SnapshotLinks) > 0 || !reflect.ValueOf(row.Record.Envelope).IsZero() {
		return fmt.Errorf("held row carries a record")
	}
	return nil
}

type transferIdentityPlan struct {
	row          *sharedResult
	recipe, self digest.Digest
	inputs       []digest.Digest
	provenance   []egraphInputProvenanceKind
}
