package httpdns

import (
	"strconv"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
)

const AttrNetConfig = "httpdns.netconfig"

// HTTP is a helper mimicking the llb.HTTP function, but with the ability to
// set additional attributes.
func State(url string, netConfID string, opts ...llb.HTTPOption) llb.State {
	hi := &llb.HTTPInfo{}
	for _, o := range opts {
		o.SetHTTPOption(hi)
	}

	attrs := map[string]string{
		AttrNetConfig: netConfID,
	}
	if hi.Checksum != "" {
		attrs[pb.AttrHTTPChecksum] = hi.Checksum.String()
	}
	if hi.Filename != "" {
		attrs[pb.AttrHTTPFilename] = hi.Filename
	}
	if hi.Perm != 0 {
		attrs[pb.AttrHTTPPerm] = "0" + strconv.FormatInt(int64(hi.Perm), 8)
	}
	if hi.UID != 0 {
		attrs[pb.AttrHTTPUID] = strconv.Itoa(hi.UID)
	}
	if hi.GID != 0 {
		attrs[pb.AttrHTTPGID] = strconv.Itoa(hi.GID)
	}

	source := llb.NewSource(url, attrs, hi.Constraints)
	return llb.NewState(source.Output())
}
