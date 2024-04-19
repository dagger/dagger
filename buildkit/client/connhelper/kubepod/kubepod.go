// Package kubepod provides connhelper for kube-pod://<pod>
package kubepod

import (
	"context"
	"net"
	"net/url"
	"regexp"

	"github.com/docker/cli/cli/connhelper/commandconn"
	"github.com/moby/buildkit/client/connhelper"
	"github.com/pkg/errors"
)

func init() {
	connhelper.Register("kube-pod", Helper)
}

// Helper returns helper for connecting to a Kubernetes pod.
// Requires BuildKit v0.5.0 or later in the pod.
func Helper(u *url.URL) (*connhelper.ConnectionHelper, error) {
	sp, err := SpecFromURL(u)
	if err != nil {
		return nil, err
	}
	return &connhelper.ConnectionHelper{
		ContextDialer: func(ctx context.Context, addr string) (net.Conn, error) {
			// using background context because context remains active for the duration of the process, after dial has completed
			return commandconn.New(context.Background(), "kubectl", "--context="+sp.Context, "--namespace="+sp.Namespace,
				"exec", "--container="+sp.Container, "-i", sp.Pod, "--", "buildctl", "dial-stdio")
		},
	}, nil
}

// Spec
type Spec struct {
	Context   string
	Namespace string
	Pod       string
	Container string
}

// SpecFromURL creates Spec from URL.
// URL is like kube-pod://<pod>?context=<context>&namespace=<namespace>&container=<container> .
// Only <pod> part is mandatory.
func SpecFromURL(u *url.URL) (*Spec, error) {
	q := u.Query()
	sp := Spec{
		Context:   q.Get("context"),
		Namespace: q.Get("namespace"),
		Pod:       u.Hostname(),
		Container: q.Get("container"),
	}
	if sp.Namespace != "" && !validKubeIdentifier(sp.Namespace) {
		return nil, errors.Errorf("unsupported namespace name: %q", sp.Namespace)
	}
	if sp.Pod == "" {
		return nil, errors.New("url lacks pod name")
	}
	if !validKubeIdentifier(sp.Pod) {
		return nil, errors.Errorf("unsupported pod name: %q", sp.Pod)
	}
	if sp.Container != "" && !validKubeIdentifier(sp.Container) {
		return nil, errors.Errorf("unsupported container name: %q", sp.Container)
	}
	return &sp, nil
}

var kubeIdentifierRegexp = regexp.MustCompile(`^[-a-z0-9.]+$`)

// validKubeIdentifier: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names
// The length is not checked because future version of Kube may support longer identifiers.
func validKubeIdentifier(s string) bool {
	return kubeIdentifierRegexp.MatchString(s)
}
