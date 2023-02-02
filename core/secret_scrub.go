package core

// SecretToScrubInfo stores the info to access secrets and scrub them from outputs.
type SecretToScrubInfo struct {
	// Envs stores environment variable names that we need to scrub.
	Envs []string `json:"envs,omitempty"`

	// Files stores secret file paths that we need to scrub.
	Files []string `json:"files,omitempty"`
}
