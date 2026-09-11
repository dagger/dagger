package dagql

import (
	"encoding/json"
)

// persistTestSnapshotRoleVisitor declares the test snapshot value's single
// output storage role.
type persistTestSnapshotRoleVisitor struct{}

func (persistTestSnapshotRoleVisitor) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	return v.Payload, nil
}

func init() {
	for _, family := range []PersistedObjectFamily{
		{Name: "dagql_test.PersistCodecObj", Typed: (*persistCodecObj)(nil), Visitor: PersistedNoReferences{}},
		{Name: "dagql_test.PersistConcurrentDecodeObj", Typed: (*persistConcurrentDecodeObj)(nil), Visitor: PersistedNoReferences{}},
		{Name: "dagql_test.PersistRetryDecodeObj", Typed: (*persistRetryDecodeObj)(nil), Visitor: PersistedNoReferences{}},
		{Name: "dagql_test.PersistResourceScopedObj", Typed: (*persistResourceScopedObj)(nil), Visitor: PersistedNoReferences{}},
		{Name: "dagql_test.PersistSnapshotValue", Typed: (*persistSnapshotValue)(nil), Visitor: persistTestSnapshotRoleVisitor{}},
	} {
		RegisterPersistedObjectFamily(family)
	}
}
