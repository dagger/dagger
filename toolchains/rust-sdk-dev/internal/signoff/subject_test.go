package signoff

import "testing"

func TestAdmitArtifactSubject(t *testing.T) {
	t.Parallel()
	const revision = "0123456789abcdef0123456789abcdef01234567"
	subject, err := AdmitArtifactSubject(
		"https://github.com/iw/dagger.git",
		[]byte(`{"subject":{"repository":"https://github.com/iw/dagger","revision":"`+revision+`"}}`),
	)
	if err != nil {
		t.Fatalf("admit immutable subject: %v", err)
	}
	if subject.Repository != "https://github.com/iw/dagger" || subject.Revision != revision {
		t.Fatalf("unexpected subject: %#v", subject)
	}
}

func TestArtifactSeedMustMatchTheImmutableSubjectBytes(t *testing.T) {
	t.Parallel()
	seed := []byte("{\n  \"subject\": \"immutable\"\n}\n")
	if err := VerifyArtifactSeed(seed, append([]byte(nil), seed...)); err != nil {
		t.Fatalf("matching immutable seed rejected: %v", err)
	}
	for name, rederived := range map[string][]byte{
		"stale value":  []byte("{\n  \"subject\": \"stale\"\n}\n"),
		"live drift":   append(append([]byte(nil), seed...), ' '),
		"missing seed": nil,
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifyArtifactSeed(seed, rederived); err == nil {
				t.Fatalf("%s was admitted", name)
			}
		})
	}
}

func TestArtifactPlanSubjectBindsTheTopLevelRevisionToTheCanonicalRepository(t *testing.T) {
	t.Parallel()
	const revision = "0123456789abcdef0123456789abcdef01234567"
	subject, err := AdmitArtifactPlanSubject(
		"https://github.com/iw/dagger.git",
		[]byte(`{"subject_revision":"`+revision+`","artifact_plan":{"subject":{"repository":"https://github.com/iw/dagger","revision":"`+revision+`"}}}`),
	)
	if err != nil {
		t.Fatalf("admit run-plan subject: %v", err)
	}
	if subject.Repository != "https://github.com/iw/dagger" || subject.Revision != revision {
		t.Fatalf("unexpected run-plan subject: %#v", subject)
	}
	if _, err := AdmitArtifactPlanSubject(
		"https://github.com/iw/dagger",
		[]byte(`{"subject_revision":"main","artifact_plan":{"subject":{"repository":"https://github.com/iw/dagger","revision":"main"}}}`),
	); err == nil {
		t.Fatal("mutable run-plan subject was admitted")
	}
	if _, err := AdmitArtifactPlanSubject(
		"https://github.com/iw/dagger",
		[]byte(`{"subject_revision":"`+revision+`","artifact_plan":{"subject":{"repository":"https://mirror.example/dagger","revision":"`+revision+`"}}}`),
	); err == nil {
		t.Fatal("alternate credential-free mirror was admitted")
	}
}

func TestAdmitArtifactSubjectRejectsMutableOrCredentialedCoordinates(t *testing.T) {
	t.Parallel()
	const validSeed = `{"subject":{"repository":"https://github.com/iw/dagger","revision":"0123456789abcdef0123456789abcdef01234567"}}`
	for name, repository := range map[string]string{
		"ssh":         "git@github.com:iw/dagger.git",
		"credentials": "https://token@github.com/iw/dagger.git",
		"query":       "https://github.com/iw/dagger.git?token=secret",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := AdmitArtifactSubject(repository, []byte(validSeed)); err == nil {
				t.Fatalf("accepted unsafe repository %q", repository)
			}
		})
	}
	for name, seed := range map[string]string{
		"short":     `{"subject":{"repository":"https://github.com/iw/dagger","revision":"main"}}`,
		"uppercase": `{"subject":{"repository":"https://github.com/iw/dagger","revision":"0123456789abcdef0123456789abcdef0123456A"}}`,
		"mirror":    `{"subject":{"repository":"https://mirror.example/dagger","revision":"0123456789abcdef0123456789abcdef01234567"}}`,
		"missing":   `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := AdmitArtifactSubject("https://github.com/iw/dagger", []byte(seed)); err == nil {
				t.Fatalf("accepted unsafe seed %s", seed)
			}
		})
	}
}
