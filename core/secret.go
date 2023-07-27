package core

// Secret is a content-addressed secret.
type Secret struct {
	// Name specifies the arbitrary name/id of the secret.
	Name string `json:"name,omitempty"`
}

// SecretID is an opaque value representing a content-addressed secret.
type SecretID string

func NewDynamicSecret(name string) *Secret {
	return &Secret{
		Name: name,
	}
}

func (id SecretID) ToSecret() (*Secret, error) {
	var secret Secret
	if err := decodeID(&secret, id); err != nil {
		return nil, err
	}

	return &secret, nil
}

func (id SecretID) String() string { return string(id) }

func (secret *Secret) Clone() *Secret {
	cp := *secret
	return &cp
}

func (secret *Secret) ID() (SecretID, error) {
	return encodeID[SecretID](secret)
}
