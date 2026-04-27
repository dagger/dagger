package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/traceexec"
	"github.com/dagger/dagger/internal/buildkit/client/connhelper/kubepod"
)

type kubePod struct {
	*dialDriver
}

func init() {
	register("kube-pod", kubePod{&dialDriver{kubepod.Helper, nil}})
}

func (k kubePod) Available(ctx context.Context) (bool, error) {
	// TODO(frantjc): Ideally this would only report true if `kubectl` were on PATH and had
	// the permissions to exec into a Pod, but we leave it like this for backwards-compatbility.
	return k.dialDriver.Available(ctx)
}

func (k kubePod) ImageLoader(ctx context.Context) imageload.Backend {
	return k.dialDriver.ImageLoader(ctx)
}

func (k kubePod) Provision(ctx context.Context, u *url.URL, opts *DriverOpts) (Connector, error) {
	// If the URL has a path beyond the default "/" from url.Parse, treat it as an image reference.
	// This is backwards compatible for correct uses of this driver because "kube-pod://my-pod-name"
	// cannot have a slash in it, else it is not a valid Pod name. Technically, this driver was
	// ignoring the ":port/path" parts of the URL previously, so users could have been using
	// it incorrectly, thought our existing documentation would not have led them to do this.
	if len(u.Path) > 1 {
		imageRef := u.Host + u.Path
		slog := slog.SpanLogger(ctx, InstrumentationLibrary)

		// Parse namespace from query params, default to "default"
		namespace := u.Query().Get("namespace")
		if namespace == "" {
			namespace = "default"
		}

		// Parse environment variables from query params
		var envVars []corev1.EnvVar
		if envParam := u.Query().Get("env"); envParam != "" {
			for _, env := range strings.Split(envParam, ",") {
				parts := strings.SplitN(env, "=", 2)
				if len(parts) == 2 {
					envVars = append(envVars, corev1.EnvVar{
						Name:  parts[0],
						Value: parts[1],
					})
				}
			}
		}

		// Add DAGGER_CLOUD_TOKEN if present in opts
		if opts.DaggerCloudToken != "" {
			envVars = append(envVars, corev1.EnvVar{
				Name:  EnvDaggerCloudToken,
				Value: opts.DaggerCloudToken,
			})
		}

		// Determine cleanup behavior
		cleanup := true
		if val, ok := os.LookupEnv("DAGGER_LEAVE_OLD_ENGINE"); ok {
			b, _ := strconv.ParseBool(val)
			cleanup = !b
		} else if val := u.Query().Get("cleanup"); val != "" {
			cleanup, _ = strconv.ParseBool(val)
		}

		podName := u.Query().Get("pod")
		if podName == "" {
			id, err := resolveImageID(imageRef)
			if err != nil {
				return nil, err
			}
			// run the Pod using that id in the name
			podName = containerNamePrefix + id
		}

		// Collect leftover engine pods
		leftoverEngines, err := k.collectLeftoverEngines(ctx, namespace, podName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			slog.Warn("failed to list pods", "error", err)
			leftoverEngines = []string{}
		}

		// Check if pod already exists
		for i, leftoverEngine := range leftoverEngines {
			// If we already have a pod with the resolved name, reuse it.
			if leftoverEngine == podName {
				k.garbageCollectEngines(ctx, namespace, cleanup, slices.Delete(leftoverEngines, i, i+1))
				u = &url.URL{
					Scheme:   u.Scheme,
					Host:     podName,
					RawQuery: u.RawQuery,
				}
				return k.dialDriver.Provision(ctx, u, opts)
			}
		}

		if err := k.createPod(ctx, namespace, podName, imageRef, envVars, opts); err != nil {
			return nil, fmt.Errorf("failed to create pod: %w", err)
		}

		if err := k.waitForPod(ctx, namespace, podName); err != nil {
			return nil, fmt.Errorf("failed waiting for pod: %w", err)
		}

		k.garbageCollectEngines(ctx, namespace, cleanup, leftoverEngines)

		// Modify URL to point the underlying dialDriver to the Pod.
		u = &url.URL{
			Scheme:   u.Scheme,
			Host:     podName,
			RawQuery: u.RawQuery,
		}
	}
	return k.dialDriver.Provision(ctx, u, opts)
}

func (k kubePod) createPod(ctx context.Context, namespace, podName, image string, envVars []corev1.EnvVar, opts *DriverOpts) error {
	volume := corev1.Volume{
		Name: "dagger",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
	container := corev1.Container{
		Name:  "dagger-engine",
		Image: image,
		Args:  []string{"--debug"},
		SecurityContext: &corev1.SecurityContext{
			Privileged: ptr(true),
		},
		Env: envVars,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      volume.Name,
				MountPath: "/var/lib/dagger",
			},
		},
	}

	// Add GPU resource request if GPU support is enabled
	if opts.GPUSupport != "" {
		container.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			},
		}
	}

	pod := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{container},
			Volumes: []corev1.Volume{volume},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	manifest, err := json.Marshal(pod)
	if err != nil {
		return fmt.Errorf("failed to marshal pod manifest: %w", err)
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(string(manifest))
	return traceexec.Exec(ctx, cmd)
}

func ptr[T any](v T) *T {
	return &v
}

func (k kubePod) waitForPod(ctx context.Context, namespace, podName string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "wait",
		"--for=condition=ready",
		"pod", podName,
		"-n", namespace)
	return traceexec.Exec(ctx, cmd)
}

func (k kubePod) collectLeftoverEngines(ctx context.Context, namespace string, additionalNames ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "pods",
		"-n", namespace,
		"-o", "jsonpath={.items[*].metadata.name}")
	stdout, _, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	if stdout == "" {
		return nil, nil
	}

	pods := strings.Fields(stdout)
	var filteredPods []string
	for _, pod := range pods {
		if strings.HasPrefix(pod, containerNamePrefix) || slices.Contains(additionalNames, pod) {
			filteredPods = append(filteredPods, pod)
		}
	}
	return filteredPods, nil
}

func (k kubePod) garbageCollectEngines(ctx context.Context, namespace string, cleanup bool, pods []string) {
	if !cleanup {
		return
	}
	for _, pod := range pods {
		if pod == "" {
			continue
		}
		cmd := exec.CommandContext(ctx, "kubectl", "delete", "pod", pod, "-n", namespace)
		if err := traceexec.Exec(ctx, cmd); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}
