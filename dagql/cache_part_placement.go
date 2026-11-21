package dagql

import (
	"slices"
)

// The placement rule applies wherever a part is placed on an entry other
// than the one that stored it: a part a stored bundle borrows from another
// entry of the class. A part's value can name service bindings by entry
// number, and an offer's owner must list every entry the part names, so
// each service reference is mapped to a service entry the receiver has, of
// the same recipe or class. The owner lists exactly those references and
// nothing else: the receiving entry's own requirements govern every hit on
// it. A part with a service that can't be mapped is not placed.

// placeOwnedPart applies the placement rule to placed, the caller's own copy
// of a part, which it changes and returns, with place mapping a service
// entry's number to the receiver's entry for it: the part with its services
// relocated and an owner of exactly those services, or false when a service
// can't be mapped: the part is not placed.
func placeOwnedPart(placed PersistedPartOffer, place func(service uint64) (uint64, bool)) (PersistedPartOffer, bool) {
	placed.Owner.DependencyIDs = nil
	for i, binding := range placed.Value.Services {
		mapped, ok := place(binding.ServiceResultID)
		if !ok {
			return PersistedPartOffer{}, false
		}
		placed.Value.Services[i].ServiceResultID = mapped
		placed.Owner.DependencyIDs = append(placed.Owner.DependencyIDs, mapped)
	}
	slices.Sort(placed.Owner.DependencyIDs)
	placed.Owner.DependencyIDs = slices.Compact(placed.Owner.DependencyIDs)
	return placed, true
}
