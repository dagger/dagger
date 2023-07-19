package gitdns

import (
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/moby/buildkit/util/sshutil"
)

const AttrNetConfig = "gitdns.netconfig"

// Git is a helper mimicking the llb.Git function, but with the ability to
// set additional attributes.
func State(remote, ref, netConfID string, opts ...llb.GitOption) llb.State {
	hi := &llb.GitInfo{}
	for _, o := range opts {
		o.SetGitOption(hi)
	}

	attrs := map[string]string{
		AttrNetConfig: netConfID,
	}
	url := strings.Split(remote, "#")[0]

	var protocolType int
	remote, protocolType = gitutil.ParseProtocol(remote)

	var sshHost string
	if protocolType == gitutil.SSHProtocol {
		parts := strings.SplitN(remote, ":", 2)
		if len(parts) == 2 {
			sshHost = parts[0]
			// keep remote consistent with http(s) version
			remote = parts[0] + "/" + parts[1]
		}
	}
	if protocolType == gitutil.UnknownProtocol {
		url = "https://" + url
	}

	id := remote

	if ref != "" {
		id += "#" + ref
	}

	gi := &llb.GitInfo{
		AuthHeaderSecret: "GIT_AUTH_HEADER",
		AuthTokenSecret:  "GIT_AUTH_TOKEN",
	}
	for _, o := range opts {
		o.SetGitOption(gi)
	}
	if gi.KeepGitDir {
		attrs[pb.AttrKeepGitDir] = "true"
	}
	if url != "" {
		attrs[pb.AttrFullRemoteURL] = url
	}
	if gi.AuthTokenSecret != "" {
		attrs[pb.AttrAuthTokenSecret] = gi.AuthTokenSecret
	}
	if gi.AuthHeaderSecret != "" {
		attrs[pb.AttrAuthHeaderSecret] = gi.AuthHeaderSecret
	}
	if protocolType == gitutil.SSHProtocol {
		if gi.KnownSSHHosts != "" {
			attrs[pb.AttrKnownSSHHosts] = gi.KnownSSHHosts
		} else if sshHost != "" {
			keyscan, err := sshutil.SSHKeyScan(sshHost)
			if err == nil {
				// best effort
				attrs[pb.AttrKnownSSHHosts] = keyscan
			}
		}

		if gi.MountSSHSock == "" {
			attrs[pb.AttrMountSSHSock] = "default"
		} else {
			attrs[pb.AttrMountSSHSock] = gi.MountSSHSock
		}
	}

	source := llb.NewSource("git://"+id, attrs, gi.Constraints)
	return llb.NewState(source.Output())
}
