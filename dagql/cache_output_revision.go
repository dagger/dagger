package dagql

import "fmt"

// OutputRevision identifies one typed output publication within this process.
// It is never part of a persisted or transferred identity.
type OutputRevision uint64

// PersistedOutputVersion reads under a nonblocking core persistence guard.
// Busy bodies and publications return ErrPersistStateNotReady.
type PersistedOutputVersion interface {
	PersistedOutputRevision() (OutputRevision, error)
}

// SnapshotOwnerReader reads a coherent revision and link set while waiting on
// the value's publication/body latches. Only owner synchronization, outside
// graph locks, may use it. Capture, boot and import keep the nonblocking reads.
type SnapshotOwnerReader interface {
	ReadSnapshotOwner() (OutputRevision, []PersistedSnapshotRefLink, error)
}

type snapshotOwnerVersion struct{ SnapshotOwnerReader }

func (v snapshotOwnerVersion) PersistedOutputRevision() (OutputRevision, error) {
	revision, _, err := v.ReadSnapshotOwner()
	return revision, err
}

type capturedOutputVersionsKey struct{}
type capturedOutputVersion struct {
	value    PersistedOutputVersion
	revision OutputRevision
}
type capturedOutputVersions []capturedOutputVersion

func (versions *capturedOutputVersions) record(value PersistedOutputVersion) error {
	revision, err := value.PersistedOutputRevision()
	if err != nil {
		return err
	}
	*versions = append(*versions, capturedOutputVersion{value, revision})
	return nil
}

func (versions *capturedOutputVersions) check() error {
	for _, version := range *versions {
		revision, err := version.value.PersistedOutputRevision()
		if err != nil {
			return err
		}
		if revision != version.revision {
			return fmt.Errorf("%w: typed output changed during capture", ErrPersistStateNotReady)
		}
	}
	return nil
}

// The encoded representation and desired links are one immutable payload
// snapshot. Typed outputs additionally carry their own core publication stamp.
type capturedRowRevision struct {
	payload sharedResultPayloadState
	outputs capturedOutputVersions
}

func (version *capturedRowRevision) check(row *sharedResult) error {
	if err := version.outputs.check(); err != nil {
		return err
	}
	row.payloadMu.RLock()
	same := row.payloadRevision == version.payload.payloadRevision &&
		row.persistedEnvelope == version.payload.persistedEnvelope &&
		row.hasValue == version.payload.hasValue && row.isObject == version.payload.isObject
	row.payloadMu.RUnlock()
	if !same {
		return fmt.Errorf("%w: row %d representation changed during capture", ErrPersistStateNotReady, row.id)
	}
	return nil
}
