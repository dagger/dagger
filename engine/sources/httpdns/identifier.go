package httpdns

import (
	bkhttp "github.com/moby/buildkit/source/http"
)

const AttrDNSNamespace = "dagger.dns.namespace"

type HTTPIdentifier struct {
	bkhttp.HTTPIdentifier

	Namespace string
}
