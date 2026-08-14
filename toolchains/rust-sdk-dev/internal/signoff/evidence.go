package signoff

import "fmt"

// SecretEvidenceDomain is one exact-run byte domain required by Rust secret admission.
// Values deliberately match the Rust CLI's fixed evidence filenames.
type SecretEvidenceDomain string

const (
	SecretEvidenceSourceFiles            SecretEvidenceDomain = "source-files"
	SecretEvidenceGeneratedPackagedFiles SecretEvidenceDomain = "generated-and-packaged-files"
	SecretEvidenceArtifactEntries        SecretEvidenceDomain = "artifact-entries"
	SecretEvidenceCacheAndProvenance     SecretEvidenceDomain = "cache-and-provenance"
	SecretEvidenceProcessOutput          SecretEvidenceDomain = "process-output"
	SecretEvidenceErrorsAndDebug         SecretEvidenceDomain = "errors-and-debug"
	SecretEvidenceDiagnosticsAndTraces   SecretEvidenceDomain = "diagnostics-and-traces"
	SecretEvidenceReports                SecretEvidenceDomain = "reports"
	SecretEvidenceDraftVerdict           SecretEvidenceDomain = "draft-verdict"
)

const (
	secretEvidenceMebibyte = 1024 * 1024
)

// SecretEvidenceFile retains actual bytes for one required inspection domain.
type SecretEvidenceFile struct {
	Domain SecretEvidenceDomain
	Bytes  []byte
}

// SecretEvidenceDomains is the closed set of actual exact-run evidence supplied to Rust.
// A digest or availability marker cannot satisfy a domain because every field must retain bytes.
type SecretEvidenceDomains struct {
	SourceFiles            []byte
	GeneratedPackagedFiles []byte
	ArtifactEntries        []byte
	CacheAndProvenance     []byte
	ProcessOutput          []byte
	ErrorsAndDebug         []byte
	DiagnosticsAndTraces   []byte
	Reports                []byte
	DraftVerdict           []byte
}

// Files returns the fixed inspection order after proving every domain is available and bounded.
func (domains SecretEvidenceDomains) Files() ([]SecretEvidenceFile, error) {
	files := []SecretEvidenceFile{
		{Domain: SecretEvidenceSourceFiles, Bytes: domains.SourceFiles},
		{Domain: SecretEvidenceGeneratedPackagedFiles, Bytes: domains.GeneratedPackagedFiles},
		{Domain: SecretEvidenceArtifactEntries, Bytes: domains.ArtifactEntries},
		{Domain: SecretEvidenceCacheAndProvenance, Bytes: domains.CacheAndProvenance},
		{Domain: SecretEvidenceProcessOutput, Bytes: domains.ProcessOutput},
		{Domain: SecretEvidenceErrorsAndDebug, Bytes: domains.ErrorsAndDebug},
		{Domain: SecretEvidenceDiagnosticsAndTraces, Bytes: domains.DiagnosticsAndTraces},
		{Domain: SecretEvidenceReports, Bytes: domains.Reports},
		{Domain: SecretEvidenceDraftVerdict, Bytes: domains.DraftVerdict},
	}
	for _, file := range files {
		limit := secretEvidenceDomainLimit(file.Domain)
		if len(file.Bytes) == 0 {
			return nil, fmt.Errorf("secret evidence domain %q is unavailable", file.Domain)
		}
		if uint64(len(file.Bytes)) > limit {
			return nil, fmt.Errorf("secret evidence domain %q exceeds its %d-byte bound", file.Domain, limit)
		}
	}
	return files, nil
}

func secretEvidenceDomainLimit(domain SecretEvidenceDomain) uint64 {
	switch domain {
	case SecretEvidenceSourceFiles,
		SecretEvidenceGeneratedPackagedFiles,
		SecretEvidenceArtifactEntries,
		SecretEvidenceErrorsAndDebug:
		return secretEvidenceMebibyte
	case SecretEvidenceCacheAndProvenance:
		return 256 * 1024
	case SecretEvidenceProcessOutput, SecretEvidenceDiagnosticsAndTraces:
		return 4 * secretEvidenceMebibyte
	case SecretEvidenceDraftVerdict:
		return 8 * secretEvidenceMebibyte
	case SecretEvidenceReports:
		return 16 * secretEvidenceMebibyte
	default:
		return 0
	}
}
