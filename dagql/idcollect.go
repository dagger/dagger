package dagql

import (
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

// CollectIDs walks the given ID and collects all unique IDs found, grouped by their type name.
func CollectIDs(id *call.ID, skipTopLevel bool) (*IDSet, error) {
	ids := &IDSet{
		typeNameToIDs: map[string][]*call.ID{},
		memo:          map[digest.Digest]struct{}{},
		skip:          id,
	}
	_, err := visitID(id, ids.visit, false)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

type IDSet struct {
	typeNameToIDs map[string][]*call.ID
	memo          map[digest.Digest]struct{}
	skip          *call.ID
}

func CollectedIDs[T Typed](ids *IDSet) []ID[T] {
	var t T
	callIDs := ids.typeNameToIDs[t.Type().Name()]
	if len(callIDs) == 0 {
		return nil
	}
	typedIDs := make([]ID[T], 0, len(callIDs))
	for _, callID := range callIDs {
		typedIDs = append(typedIDs, NewID[T](callID))
	}
	return typedIDs
}

func (ids *IDSet) visit(id *call.ID) (*call.ID, error) {
	dgst := id.Digest()
	if _, ok := ids.memo[dgst]; ok {
		return nil, ErrStopVisit
	}
	ids.memo[dgst] = struct{}{}

	if id == ids.skip {
		return nil, nil
	}

	if typeName := id.Type().NamedType(); typeName != "" {
		ids.typeNameToIDs[typeName] = append(ids.typeNameToIDs[typeName], id)
	}

	return nil, nil
}
