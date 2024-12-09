package containerimagedns

import (
	"github.com/moby/buildkit/source"
)

const AttrDNSNamespace = "dagger.dns.namespace"

type Identifier struct {
	source.Identifier
	Namespace string
}
