package signoff

import "testing"

func completeSecretEvidenceDomains() SecretEvidenceDomains {
	return SecretEvidenceDomains{
		SourceFiles: []byte("source"), GeneratedPackagedFiles: []byte("generated"),
		ArtifactEntries: []byte("artifact"), CacheAndProvenance: []byte("cache"),
		ProcessOutput: []byte("process"), ErrorsAndDebug: []byte("debug"),
		DiagnosticsAndTraces: []byte("trace"), Reports: []byte("report"),
		DraftVerdict: []byte("draft"),
	}
}

func TestSecretEvidenceRequiresEveryActualDomain(t *testing.T) {
	t.Parallel()

	complete := completeSecretEvidenceDomains()
	files, err := complete.Files()
	if err != nil {
		t.Fatalf("complete secret evidence rejected: %v", err)
	}
	if len(files) != 9 {
		t.Fatalf("secret evidence has %d domains, want 9", len(files))
	}

	mutations := []func(*SecretEvidenceDomains){
		func(value *SecretEvidenceDomains) { value.SourceFiles = nil },
		func(value *SecretEvidenceDomains) { value.GeneratedPackagedFiles = nil },
		func(value *SecretEvidenceDomains) { value.ArtifactEntries = nil },
		func(value *SecretEvidenceDomains) { value.CacheAndProvenance = nil },
		func(value *SecretEvidenceDomains) { value.ProcessOutput = nil },
		func(value *SecretEvidenceDomains) { value.ErrorsAndDebug = nil },
		func(value *SecretEvidenceDomains) { value.DiagnosticsAndTraces = nil },
		func(value *SecretEvidenceDomains) { value.Reports = nil },
		func(value *SecretEvidenceDomains) { value.DraftVerdict = nil },
	}
	for index, mutate := range mutations {
		candidate := complete
		mutate(&candidate)
		if _, err := candidate.Files(); err == nil {
			t.Fatalf("unavailable secret evidence domain %d was admitted", index)
		}
	}
}

func TestSecretEvidenceRejectsBytesBeyondEachDomainBound(t *testing.T) {
	t.Parallel()

	for _, domain := range []SecretEvidenceDomain{
		SecretEvidenceSourceFiles, SecretEvidenceGeneratedPackagedFiles,
		SecretEvidenceArtifactEntries, SecretEvidenceCacheAndProvenance,
		SecretEvidenceProcessOutput, SecretEvidenceErrorsAndDebug,
		SecretEvidenceDiagnosticsAndTraces, SecretEvidenceReports, SecretEvidenceDraftVerdict,
	} {
		candidate := completeSecretEvidenceDomains()
		oversized := make([]byte, secretEvidenceDomainLimit(domain)+1)
		switch domain {
		case SecretEvidenceSourceFiles:
			candidate.SourceFiles = oversized
		case SecretEvidenceGeneratedPackagedFiles:
			candidate.GeneratedPackagedFiles = oversized
		case SecretEvidenceArtifactEntries:
			candidate.ArtifactEntries = oversized
		case SecretEvidenceCacheAndProvenance:
			candidate.CacheAndProvenance = oversized
		case SecretEvidenceProcessOutput:
			candidate.ProcessOutput = oversized
		case SecretEvidenceErrorsAndDebug:
			candidate.ErrorsAndDebug = oversized
		case SecretEvidenceDiagnosticsAndTraces:
			candidate.DiagnosticsAndTraces = oversized
		case SecretEvidenceReports:
			candidate.Reports = oversized
		case SecretEvidenceDraftVerdict:
			candidate.DraftVerdict = oversized
		}
		if _, err := candidate.Files(); err == nil {
			t.Fatalf("oversized secret evidence domain %q was admitted", domain)
		}
	}
}
