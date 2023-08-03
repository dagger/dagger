package httpdns

import (
	"fmt"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	srctypes "github.com/moby/buildkit/source/types"
)

const AttrNetConfig = "httpdns.netconfig"

// State is a helper mimicking the llb.HTTP function, but with the ability to
// set additional attributes.
func State(url string, clientIDs []string, opts ...llb.HTTPOption) llb.State {
	hack, err := buildkit.EncodeIDHack(DaggerHTTPURLHack{
		URL:       url,
		ClientIDs: clientIDs,
	})
	if err != nil {
		panic(err)
	}

	opts = append(opts, llb.WithCustomName(url))

	// has to start with https:// for buildkit to recognize the scheme and
	// associate it to the source
	url = fmt.Sprintf("%s://%s", srctypes.HTTPSScheme, hack)

	return llb.HTTP(url, opts...)
}
