package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"slices"

	"github.com/opencontainers/go-digest"
)

const (
	sshAuthSocketDigestVersion = "ssh-auth-socket-v1"
)

func ScopedSSHAuthSocketDigest(secretSalt []byte, fingerprints []string) digest.Digest {
	slices.Sort(fingerprints)

	mac := hmac.New(sha256.New, secretSalt)
	mac.Write([]byte(sshAuthSocketDigestVersion))
	mac.Write([]byte{0})
	for _, fingerprint := range fingerprints {
		mac.Write([]byte(fingerprint))
		mac.Write([]byte{0})
	}
	return digest.NewDigestFromBytes(digest.SHA256, mac.Sum(nil))
}

func ScopedSSHAuthSocketDigestFromStore(
	ctx context.Context,
	query *Query,
	socketStore *SocketStore,
	sourceSocketDigest digest.Digest,
) (digest.Digest, error) {
	fingerprints, err := socketStore.AgentFingerprints(ctx, sourceSocketDigest)
	if err != nil {
		return "", err
	}
	return ScopedSSHAuthSocketDigest(query.SecretSalt(), fingerprints), nil
}
