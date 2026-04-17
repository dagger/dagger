package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
)

const sshAuthSocketDigestVersion = "ssh-auth-socket-v1"

func ScopedSSHAuthSocketHandle(secretSalt []byte, fingerprints []string) dagql.SessionResourceHandle {
	slices.Sort(fingerprints)

	mac := hmac.New(sha256.New, secretSalt)
	mac.Write([]byte(sshAuthSocketDigestVersion))
	mac.Write([]byte{0})
	for _, fingerprint := range fingerprints {
		mac.Write([]byte(fingerprint))
		mac.Write([]byte{0})
	}
	return dagql.SessionResourceHandle(digest.NewDigestFromBytes(digest.SHA256, mac.Sum(nil)))
}

func ScopedNestedSSHAuthSocketHandle(secretSalt []byte, fingerprints []string, clientID string) dagql.SessionResourceHandle {
	slices.Sort(fingerprints)

	mac := hmac.New(sha256.New, secretSalt)
	mac.Write([]byte(sshAuthSocketDigestVersion))
	mac.Write([]byte{0})
	for _, fingerprint := range fingerprints {
		mac.Write([]byte(fingerprint))
		mac.Write([]byte{0})
	}
	mac.Write([]byte(clientID))
	mac.Write([]byte{0})
	return dagql.SessionResourceHandle(digest.NewDigestFromBytes(digest.SHA256, mac.Sum(nil)))
}
