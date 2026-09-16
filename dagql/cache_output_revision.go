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
