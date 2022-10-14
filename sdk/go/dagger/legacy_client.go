package dagger

type SecretID string

type FSID string

type Filesystem struct {
	ID FSID `json:"id"`
}
