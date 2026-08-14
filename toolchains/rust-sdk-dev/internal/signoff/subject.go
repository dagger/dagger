package signoff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const maxArtifactSeedBytes = 1024 * 1024

// ArtifactSubject identifies the immutable, credential-free Git source from which
// the exact sign-off graph must be constructed.
type ArtifactSubject struct {
	Repository string
	Revision   string
}

// AdmitArtifactSubject extracts the subject revision from a canonical artifact seed
// and closes the repository boundary before any Git or target graph is constructed.
// Rust remains authoritative for the complete seed; this adapter admits only the two
// values needed to select the immutable source object.
func AdmitArtifactSubject(repository string, seed []byte) (ArtifactSubject, error) {
	if len(seed) == 0 || len(seed) > maxArtifactSeedBytes {
		return ArtifactSubject{}, fmt.Errorf("artifact subject seed exceeds its byte bound")
	}
	canonicalRepository, err := canonicalArtifactRepository(repository)
	if err != nil {
		return ArtifactSubject{}, err
	}
	var projection struct {
		Subject struct {
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(seed, &projection); err != nil {
		return ArtifactSubject{}, fmt.Errorf("decode artifact subject seed: %w", err)
	}
	if projection.Subject.Repository != canonicalRepository {
		return ArtifactSubject{}, fmt.Errorf("artifact subject seed names a different canonical repository")
	}
	if !isCommitSHA(projection.Subject.Revision) {
		return ArtifactSubject{}, fmt.Errorf("artifact subject revision must be one full lowercase hexadecimal commit")
	}
	return ArtifactSubject{Repository: canonicalRepository, Revision: projection.Subject.Revision}, nil
}

// AdmitArtifactPlanSubject extracts only the immutable coordinate needed to select the policy
// implementation that performs complete Rust admission. No other plan field is trusted here.
func AdmitArtifactPlanSubject(repository string, plan []byte) (ArtifactSubject, error) {
	if len(plan) == 0 || len(plan) > maxArtifactSeedBytes {
		return ArtifactSubject{}, fmt.Errorf("artifact run plan exceeds its byte bound")
	}
	var projection struct {
		SubjectRevision string `json:"subject_revision"`
		ArtifactPlan    struct {
			Subject struct {
				Repository string `json:"repository"`
				Revision   string `json:"revision"`
			} `json:"subject"`
		} `json:"artifact_plan"`
	}
	if err := json.Unmarshal(plan, &projection); err != nil {
		return ArtifactSubject{}, fmt.Errorf("decode artifact run-plan subject: %w", err)
	}
	if projection.SubjectRevision != projection.ArtifactPlan.Subject.Revision {
		return ArtifactSubject{}, fmt.Errorf("artifact run-plan subject differs from its artifact plan")
	}
	seed, err := json.Marshal(struct {
		Subject struct {
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
		} `json:"subject"`
	}{Subject: struct {
		Repository string `json:"repository"`
		Revision   string `json:"revision"`
	}{Repository: projection.ArtifactPlan.Subject.Repository, Revision: projection.SubjectRevision}})
	if err != nil {
		return ArtifactSubject{}, fmt.Errorf("project artifact run-plan subject: %w", err)
	}
	return AdmitArtifactSubject(repository, seed)
}

func canonicalArtifactRepository(repository string) (string, error) {
	repository = strings.TrimSuffix(strings.TrimSuffix(repository, "/"), ".git")
	parsed, err := url.Parse(repository)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Path == "" || parsed.Path == "/" || strings.HasSuffix(parsed.Path, "/") ||
		strings.HasSuffix(parsed.Path, ".git") {
		return "", fmt.Errorf("artifact subject repository must be a canonical credential-free HTTPS URL")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("artifact subject repository path is not canonical")
		}
	}
	canonical := "https://" + strings.ToLower(parsed.Hostname()) + parsed.EscapedPath()
	return canonical, nil
}

// VerifyArtifactSeed requires the caller-supplied seed to be byte-identical to the seed
// independently derived by the subject revision's own policy tool. Semantic JSON equality is
// insufficient because it would let stale or non-canonical bytes acquire the immutable tree's
// authority.
func VerifyArtifactSeed(supplied, rederived []byte) error {
	if len(rederived) == 0 || len(rederived) > maxArtifactSeedBytes || !bytes.Equal(supplied, rederived) {
		return fmt.Errorf("artifact plan seed differs from the immutable subject tree")
	}
	return nil
}

func isCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
