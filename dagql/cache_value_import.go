package dagql

import (
	"fmt"
	"slices"

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

//nolint:gocyclo // one check per bundle field and record kind; splitting hides the order of the checks
func checkValueBundle(bundle ValueBundle, shapeOnly bool) ([]uint64, error) {
	if bundle.Version != valueBundleVersion {
		return nil, fmt.Errorf("unsupported value bundle version %d", bundle.Version)
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
		validate := validateTransferRecord
		if shapeOnly {
			validate = validateTransferRecordShape
		}
		if err := validate(row.Record, row.DependencyIDs); err != nil {
			return nil, fmt.Errorf("row %d: %w", id, err)
		}
		rows[id] = row
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
		if shapeOnly && !named[id] && len(row.Record.Envelope.PendingOffers) == 0 {
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
		if _, ok := rows[id]; !ok {
			return nil, fmt.Errorf("output names missing ordinal %d", id)
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

type transferIdentityPlan struct {
	row          *sharedResult
	recipe, self digest.Digest
	inputs       []digest.Digest
	provenance   []egraphInputProvenanceKind
}
