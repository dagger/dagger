package containerimagedns

import (
	bkimg "github.com/moby/buildkit/source/containerimage"
)

const AttrDNSNamespace = "dagger.dns.namespace"

type ImageIdentifier struct {
	bkimg.ImageIdentifier

	Namespace string
}
