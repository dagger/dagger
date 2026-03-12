package dagql

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestPersistInputProvenanceValidateResult(t *testing.T) {
	prov := cachePersistInputProvenance{
		Kind:   cachePersistInputProvenanceKindResult,
		Digest: "sha256:abc123",
	}
	assert.NilError(t, prov.validate())
}

func TestPersistInputProvenanceRejectsMissingPayload(t *testing.T) {
	prov := cachePersistInputProvenance{
		Kind: cachePersistInputProvenanceKindDigest,
	}
	assert.Assert(t, prov.validate() != nil)
}

func TestPersistInputProvenanceRejectsUnknownKind(t *testing.T) {
	prov := cachePersistInputProvenance{
		Kind:   cachePersistInputProvenanceKind("mystery"),
		Digest: "sha256:abc123",
	}
	assert.Assert(t, prov.validate() != nil)
}
