package nomadalloc

import (
	"context"
	"errors"
	"net"
	"net/url"

	"github.com/docker/cli/cli/connhelper/commandconn"

	"github.com/moby/buildkit/client/connhelper"
)

func init() {
	connhelper.Register("nomad-alloc", Helper)
}

// Helper returns helper for connecting to a Nomad alloc.
// Requires BuildKit v0.5.0 or later in the alloc.

// inspired by github.com/moby/buildkit/client/connhelper/kubepod
// using nomad exec to connect to a nomad alloc and run buildctl dial-stdio
func Helper(u *url.URL) (*connhelper.ConnectionHelper, error) {
	sp, err := SpecFromURL(u)
	if err != nil {
		return nil, err
	}

	if err != nil {
		return nil, err
	}

	return &connhelper.ConnectionHelper{
		ContextDialer: func(ctx context.Context, addr string) (net.Conn, error) {
			// using background context because context remains active for the duration of the process, after dial has completed
			// for nomad

			args := []string{"alloc", "exec"}
			if sp.Alloc != "" {
				args = append(args, sp.Alloc)
			}
			if sp.Job != "" {
				args = append(args, "-job")
				args = append(args, sp.Job)
			}
			if sp.Task != "" {
				args = append(args, "-task")
				args = append(args, sp.Task)
			}
			if sp.Namespace != "" {
				args = append(args, "-namespace")
				args = append(args, sp.Namespace)
			}
			if sp.Region != "" {
				args = append(args, "-region")
				args = append(args, sp.Region)
			}
			args = append(args, "buildctl", "dial-stdio")
			return commandconn.New(context.Background(), "nomad", args...)
		},
	}, nil
}

// Nomad Spec to connect for "nomad exec"
type Spec struct {
	Alloc     string
	Job       string
	Task      string
	Namespace string
	Region    string
}

// SpecFromURL creates Spec from URL.
// URL is like nomad-alloc://<alloc>?namespace=<namespace>&region=<region>&task=<task>&job=<job> .
// either <alloc> or <job> is mandatory.
func SpecFromURL(u *url.URL) (*Spec, error) {
	q := u.Query()
	sp := Spec{
		Alloc: u.Hostname(),

		Namespace: q.Get("namespace"),
		Region:    q.Get("region"),
		Task:      q.Get("task"),
		Job:       q.Get("job"),
	}

	if sp.Alloc != "" && sp.Job != "" {
		return nil, errors.New("url should not have both alloc and job")
	}
	if sp.Alloc == "" && sp.Job == "" {
		return nil, errors.New("url should have either alloc or job")
	}

	return &sp, nil
}
