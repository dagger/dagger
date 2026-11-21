package drivers

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/dagger/dagger/util/traceexec"
	telemetry "github.com/dagger/otel-go"
)

// access is a Kubernetes permission that the driver needs, in the form that
// `kubectl auth can-i` takes it.
type access struct {
	verb     string
	resource string
}

// checkAccess verifies that the cluster is reachable and that the current
// user may do everything in needs, in the given scope.
func checkAccess(ctx context.Context, sc scope, needs ...access) error {
	scope := sc.String()
	for _, need := range needs {
		args := append(sc.args(), "auth", "can-i", need.verb, need.resource)
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		// `auth can-i` exits non-zero both for "no" and for failing to ask, so
		// the answer on stdout is what tells them apart.
		stdout, stderr, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
		answer := strings.ToLower(strings.TrimSpace(stdout))
		switch {
		case strings.HasPrefix(answer, "yes"):
		case strings.HasPrefix(answer, "no"):
			return fmt.Errorf("not permitted to %s %s in %s", need.verb, need.resource, scope)
		case err != nil:
			return fmt.Errorf("cluster in %s is not reachable: %w%s", scope, err, stderrDetail(stderr))
		default:
			return fmt.Errorf("unexpected answer %q checking permission to %s %s in %s", answer, need.verb, need.resource, scope)
		}
	}
	return nil
}

// scope selects the cluster and namespace that kubectl talks to. Empty fields
// leave kubectl's own defaults: $KUBECONFIG or ~/.kube/config, its current
// context, and that context's namespace.
type scope struct {
	Kubeconfig string
	Context    string
	Namespace  string
}

// scopeFromQuery reads the scope from a driver URL's kubeconfig, context and
// namespace parameters.
func scopeFromQuery(q url.Values) scope {
	return scope{
		Kubeconfig: q.Get("kubeconfig"),
		Context:    q.Get("context"),
		Namespace:  q.Get("namespace"),
	}
}

// args are the kubectl flags that select the scope.
func (s scope) args() []string {
	var args []string
	if s.Kubeconfig != "" {
		args = append(args, "--kubeconfig="+s.Kubeconfig)
	}
	if s.Context != "" {
		args = append(args, "--context="+s.Context)
	}
	if s.Namespace != "" {
		args = append(args, "--namespace="+s.Namespace)
	}
	return args
}

func (s scope) String() string {
	desc := cmp.Or(s.Context, "the current context")
	if s.Kubeconfig != "" {
		desc += fmt.Sprintf(" of kubeconfig %q", s.Kubeconfig)
	}
	if s.Namespace != "" {
		desc += fmt.Sprintf(", namespace %q", s.Namespace)
	}
	return desc
}

func stderrDetail(stderr string) string {
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		return ": " + stderr
	}
	return ""
}
