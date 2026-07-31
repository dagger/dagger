package drivers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/client/connhelper/kubepod"
	"github.com/dagger/dagger/util/traceexec"
	telemetry "github.com/dagger/otel-go"
)

var (
	kubectlImageDriver = kubectl{&dialDriver{kubepod.Helper, nil}}
)

type kubectl struct {
	*dialDriver
}

func (k kubectl) Available(ctx context.Context) (bool, error) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return false, nil //nolint:nilerr
	}

	// Ideally we'd check that the cluster is actually usable via `kubectl cluster-info` or
	// similar,  but since the default cluster targeted by `kubectl` could be overridden by
	// `_EXPERIMENTAL_DAGGER_RUNNER_HOST=image+kubectl://foo?context=bar`, we cannot do that
	// because the interface that we are implementing does not give us access to that query
	// parameter, and I am aiming to avoid changing the interface for this feature. That
	// edge case aside, this driver is registered last amongst the image drivers, so it
	// should be OK to fall into this driver if none of the others are available and kubectl
	// is installed.
	cmd := exec.CommandContext(ctx, "kubectl", "version", "--client")
	if err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated()); err != nil {
		return false, err
	}

	return true, nil
}

func (k kubectl) ImageLoader(ctx context.Context) imageload.Backend {
	return k.dialDriver.ImageLoader(ctx)
}

func (k kubectl) Provision(ctx context.Context, u *url.URL, opts *DriverOpts) (Connector, error) {
	imageRef := u.Host + u.Path
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	q := u.Query()
	kctx := q.Get("context")
	namespace := q.Get("namespace")

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
	leftoverEngines, err := k.collectLeftoverEngines(ctx, kctx, namespace, podName)
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
			k.garbageCollectEngines(ctx, kctx, namespace, cleanup, slices.Delete(leftoverEngines, i, i+1))
			u = &url.URL{
				Scheme:   u.Scheme,
				Host:     podName,
				RawQuery: u.RawQuery,
			}
			return k.dialDriver.Provision(ctx, u, opts)
		}
	}

	if err := k.createPod(ctx, kctx, namespace, podName, imageRef, envVars, opts); err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	if err := k.waitForPod(ctx, kctx, namespace, podName); err != nil {
		return nil, fmt.Errorf("failed waiting for pod: %w", err)
	}

	k.garbageCollectEngines(ctx, kctx, namespace, cleanup, leftoverEngines)

	// Use the existing kube-pod driver from here.
	v := &url.URL{
		Scheme:   "kube-pod",
		Host:     podName,
		RawQuery: u.RawQuery,
	}

	return k.dialDriver.Provision(ctx, v, opts)
}

func (k kubectl) apply(ctx context.Context, kctx string, obj any) error {
	manifest, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal object to manifest: %w", err)
	}
	args := []string{"apply", "-f-"}
	if kctx != "" {
		args = append(args, "--context="+kctx)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = bytes.NewReader(manifest)
	return traceexec.Exec(ctx, cmd)
}

func (k kubectl) createPod(ctx context.Context, kctx, namespace, podName, image string, envVars []corev1.EnvVar, opts *DriverOpts) error {
	volume := corev1.Volume{
		Name: "dagger",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
	volumes := []corev1.Volume{volume}
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
				MountPath: distconsts.EngineDefaultStateDir,
			},
		},
	}

	secret := corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Data: map[string][]byte{},
	}

	// Add DAGGER_CLOUD_TOKEN if present in opts
	if opts.DaggerCloudToken != "" {
		cloudTokenKey := "cloudToken"
		secret.Data[cloudTokenKey] = []byte(opts.DaggerCloudToken)
		container.Env = append(container.Env, corev1.EnvVar{
			Name: EnvDaggerCloudToken,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.GetName(),
					},
					Key: cloudTokenKey,
				},
			},
		})
	}

	if _, err := os.Stat(engineCertificatesPath); err == nil {
		i := 0
		if err := filepath.WalkDir(engineCertificatesPath, func(path string, de fs.DirEntry, err error) error {
			if de.IsDir() || err != nil {
				return err
			}

			rel, err := filepath.Rel(engineCertificatesPath, path)
			if err != nil {
				return err
			}

			engineCertificateBytes, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			// Secret keys cannot contain "/". This workaround allows
			// conflicts if ~/.config/dagger/ca-certificates has e.g.
			// foo___bar.crt and foo/bar.crt, but I don't see another option.
			key := strings.ReplaceAll(rel, "/", "___")
			secret.Data[key] = engineCertificateBytes
			// When mounting a Secret to a directory, Kubernetes doesn't mount it directly.
			// Instead, it creates a versioned directory and uses symlinks. Dagger does not
			// follow these symlinks when copying these certs into containers that it runs.
			// To avoid the symlinks, we mount each file as its own volume.
			engineCertificateVolume := corev1.Volume{
				Name: fmt.Sprintf("certificate-%d", i),
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secret.GetName(),
						Items: []corev1.KeyToPath{
							{
								Key:  key,
								Path: rel,
							},
						},
					},
				},
			}
			volumes = append(volumes, engineCertificateVolume)
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      engineCertificateVolume.Name,
				ReadOnly:  true,
				SubPath:   rel,
				MountPath: filepath.Join(distconsts.EngineCustomCACertsDir, rel),
			})

			i++
			return nil
		}); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not stat certificates", "path", engineCertificatesPath, "error", err)
	}

	if len(secret.Data) > 0 {
		if err := k.apply(ctx, kctx, secret); err != nil {
			return err
		}
	}

	if _, err := os.Stat(engineConfigPath); err == nil {
		engineConfigBytes, err := os.ReadFile(engineConfigPath)
		if err != nil {
			return err
		}
		base := filepath.Base(config.DefaultConfigPath())
		configMap := corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ConfigMap",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
			},
			Data: map[string]string{
				base: string(engineConfigBytes),
			},
		}
		if err := k.apply(ctx, kctx, configMap); err != nil {
			return err
		}
		configVolume := corev1.Volume{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMap.GetName(),
					},
				},
			},
		}
		volumes = append(volumes, configVolume)
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      configVolume.Name,
			ReadOnly:  true,
			SubPath:   base,
			MountPath: config.DefaultConfigPath(),
		})
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not stat config", "path", engineConfigPath, "error", err)
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
			Containers:    []corev1.Container{container},
			Volumes:       volumes,
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	return k.apply(ctx, kctx, pod)
}

func ptr[T any](v T) *T {
	return &v
}

func (k kubectl) waitForPod(ctx context.Context, kctx, namespace, podName string) error {
	args := []string{
		"wait",
		"--for=condition=ready",
		"pod", podName,
	}
	if kctx != "" {
		args = append(args, "--context="+kctx)
	}
	if namespace != "" {
		args = append(args, "--namespace="+namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	return traceexec.Exec(ctx, cmd)
}

func (k kubectl) collectLeftoverEngines(ctx context.Context, kctx, namespace string, additionalNames ...string) ([]string, error) {
	args := []string{"get", "pods", "-o", "jsonpath={.items[*].metadata.name}"}
	if kctx != "" {
		args = append(args, "--context="+kctx)
	}
	if namespace != "" {
		args = append(args, "--namespace="+namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
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

func (k kubectl) garbageCollectEngines(ctx context.Context, kctx, namespace string, cleanup bool, engines []string) {
	if !cleanup {
		return
	}
	for _, kind := range []string{"pod", "cm", "secret"} {
		args := append([]string{"delete", kind, "--ignore-not-found"}, engines...)
		if kctx != "" {
			args = append(args, "--context="+kctx)
		}
		if namespace != "" {
			args = append(args, "--namespace="+namespace)
		}
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		if err := traceexec.Exec(ctx, cmd); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}
