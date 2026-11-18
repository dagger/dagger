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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/traceexec"
)

// kubectlImageDriver starts an engine from an image, image+kubectl://IMAGE,
// as a pod in a Kubernetes cluster, then connects to it with the kube-pod
// driver.
var kubectlImageDriver = kubectl{kubePodDriver}

type kubectl struct {
	Driver
}

// imageTarget is what the kubectl driver reads from an image+kubectl URL.
type imageTarget struct {
	// Ref is the image to run.
	Ref string
	// Name is the name of the pod to run it in.
	Name string
	// Cleanup is whether to remove engines left over from other versions.
	Cleanup bool
	// Env is the KEY=VALUE environment to run the engine with.
	Env []string
}

// parseImageTarget reads an imageTarget from an image-style URL
// (scheme://IMAGE?...) the way the image drivers do, except that nameParam
// is the query parameter that overrides the derived name.
func parseImageTarget(u *url.URL, nameParam string) (*imageTarget, error) {
	q := u.Query()
	t := &imageTarget{
		Ref:     defaultImageRef(u.Host + u.Path),
		Name:    q.Get(nameParam),
		Cleanup: cleanupOldEngines(q),
		Env:     q["env"],
	}
	if t.Name == "" {
		id, err := resolveImageID(t.Ref)
		if err != nil {
			return nil, err
		}
		t.Name = containerNamePrefix + id
	}
	return t, nil
}

// to find leftover engine pods, create the engine pod, and exec into it
var provisionAccess = []access{
	{"list", "pods"},
	{"create", "pods"},
	{"create", "pods/exec"},
}

// Available reports whether the cluster that the URL's kubeconfig, context and
// namespace parameters select is reachable and permits what Provision needs to do.
func (k kubectl) Available(ctx context.Context, u *url.URL) (bool, error) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return false, nil //nolint:nilerr
	}

	if err := checkAccess(ctx, scopeFromQuery(u.Query()), provisionAccess...); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, &UnavailableError{Reason: "kubectl: " + err.Error()}
	}

	return true, nil
}

// Provision runs the engine image as a pod, reusing an existing engine pod of
// the same name if it is healthy and was created from the same spec, and
// connects to it with the kube-pod driver.
//
// See parsePodConfig for the URL parameters that configure the pod.
func (k kubectl) Provision(ctx context.Context, u *url.URL, opts *DriverOpts) (Connector, error) {
	image, err := parseImageTarget(u, "pod")
	if err != nil {
		return nil, err
	}
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	q := u.Query()
	sc := scopeFromQuery(q)
	podName := image.Name

	cfg, err := parsePodConfig(q)
	if err != nil {
		return nil, err
	}

	objs, err := k.engineObjects(sc.Namespace, podName, image, cfg, opts)
	if err != nil {
		return nil, err
	}

	leftoverEngines, existing, err := k.collectLeftoverEngines(ctx, sc, podName)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		slog.Warn("failed to list pods", "error", err)
	}
	leftoverEngines = slices.DeleteFunc(leftoverEngines, func(name string) bool { return name == podName })

	reuse := false
	if existing != nil {
		reason := unusableReason(existing, objs.pod.Annotations[specHashAnnotation])
		if reason == "" && !podReady(existing) {
			// It may be starting, or restarting after a crash; give it the
			// chance to become ready before replacing it.
			if err := k.waitForPod(ctx, sc, podName); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil, err
				}
				reason = "it did not become ready"
			}
		}
		if reason == "" {
			reuse = true
		} else {
			slog.Info("replacing engine pod", "pod", podName, "reason", reason)
			if err := k.deletePod(ctx, sc, podName); err != nil {
				return nil, fmt.Errorf("failed to delete unusable engine pod %q (%s): %w", podName, reason, err)
			}
		}
	}

	if !reuse {
		if err := k.createEngine(ctx, sc, objs); err != nil {
			return nil, fmt.Errorf("failed to create pod: %w", err)
		}
		if err := k.waitForPod(ctx, sc, podName); err != nil {
			return nil, fmt.Errorf("failed waiting for pod: %w", err)
		}
	}

	k.garbageCollectEngines(ctx, sc, image.Cleanup, leftoverEngines)

	// Use the kube-pod driver from here.
	v := &url.URL{
		Scheme:   "kube-pod",
		Host:     podName,
		RawQuery: u.RawQuery,
	}

	return k.Driver.Provision(ctx, v, opts)
}

func (k kubectl) apply(ctx context.Context, sc scope, obj any) error {
	manifest, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal object to manifest: %w", err)
	}
	args := append(sc.args(), "apply", "-f-")
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = bytes.NewReader(manifest)
	return traceexec.Exec(ctx, cmd)
}

// engineObjects are the Kubernetes objects that make up an engine.
type engineObjects struct {
	pod       *corev1.Pod
	secret    *corev1.Secret    // nil if not needed
	configMap *corev1.ConfigMap // nil if not needed
}

func (k kubectl) createEngine(ctx context.Context, sc scope, objs *engineObjects) error {
	if objs.secret != nil {
		if err := k.apply(ctx, sc, objs.secret); err != nil {
			return err
		}
	}
	if objs.configMap != nil {
		if err := k.apply(ctx, sc, objs.configMap); err != nil {
			return err
		}
	}
	return k.apply(ctx, sc, objs.pod)
}

func (k kubectl) engineObjects(namespace, podName string, image *imageTarget, cfg *podConfig, opts *DriverOpts) (*engineObjects, error) {
	var envVars []corev1.EnvVar
	for _, env := range image.Env {
		if name, value, ok := strings.Cut(env, "="); ok {
			envVars = append(envVars, corev1.EnvVar{Name: name, Value: value})
		}
	}

	volume := corev1.Volume{
		Name:         "dagger",
		VolumeSource: cfg.Volume,
	}
	volumes := []corev1.Volume{volume}
	container := corev1.Container{
		Name:  "dagger-engine",
		Image: image.Ref,
		Args:  []string{"--debug"},
		SecurityContext: &corev1.SecurityContext{
			Privileged: new(true),
		},
		Env:          append(slices.Clone(downwardEnv), envVars...),
		Resources:    cfg.resources(opts.GPUSupport != ""),
		VolumeMounts: []corev1.VolumeMount{stateVolumeMount(volume)},
		// Same as the Helm chart's default probes.
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        engineProbeHandler,
			InitialDelaySeconds: 5,
			TimeoutSeconds:      14,
			PeriodSeconds:       15,
			SuccessThreshold:    1,
			FailureThreshold:    10,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:                  engineProbeHandler,
			InitialDelaySeconds:           5,
			TimeoutSeconds:                29,
			PeriodSeconds:                 30,
			SuccessThreshold:              1,
			FailureThreshold:              20,
			TerminationGracePeriodSeconds: new(int64(30)),
		},
	}

	objs := &engineObjects{}

	secret := &corev1.Secret{
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
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not stat certificates", "path", engineCertificatesPath, "error", err)
	}

	if len(secret.Data) > 0 {
		objs.secret = secret
	}

	if _, err := os.Stat(engineConfigPath); err == nil {
		engineConfigBytes, err := os.ReadFile(engineConfigPath)
		if err != nil {
			return nil, err
		}
		base := filepath.Base(config.DefaultConfigPath())
		objs.configMap = &corev1.ConfigMap{
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
		configVolume := corev1.Volume{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: objs.configMap.GetName(),
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

	spec := corev1.PodSpec{
		Containers: []corev1.Container{container},
		Volumes:    volumes,
		// Restart the engine in place if it crashes or is OOMKilled, keeping
		// its emptyDir state.
		RestartPolicy: corev1.RestartPolicyAlways,
	}
	hash, err := specHash(spec)
	if err != nil {
		return nil, err
	}

	objs.pod = &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   namespace,
			Annotations: map[string]string{specHashAnnotation: hash},
		},
		Spec: spec,
	}

	return objs, nil
}

// engineProbeHandler checks the engine's health with the dagger CLI that the
// engine image bundles.
var engineProbeHandler = corev1.ProbeHandler{
	Exec: &corev1.ExecAction{
		Command: []string{"sh", "-exc", "dagger core version"},
	},
}

// podReadyTimeout bounds how long Provision waits for an engine pod to pull its
// image and start. kubectl wait's own default, 30s, is shorter than a first
// pull of the engine image (~640MB) often takes.
const podReadyTimeout = "5m"

func (k kubectl) waitForPod(ctx context.Context, sc scope, podName string) error {
	args := append(sc.args(), "wait", "--for=condition=ready", "pod", podName, "--timeout="+podReadyTimeout)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	return traceexec.Exec(ctx, cmd)
}

func (k kubectl) deletePod(ctx context.Context, sc scope, podName string) error {
	args := append(sc.args(), "delete", "pod", podName, "--ignore-not-found", "--wait")
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	return traceexec.Exec(ctx, cmd)
}

// collectLeftoverEngines lists the engine pods in the namespace: those named
// with the engine instance prefix, and podName. It also returns the pod named
// podName, or nil if there is none.
func (k kubectl) collectLeftoverEngines(ctx context.Context, sc scope, podName string) ([]string, *corev1.Pod, error) {
	args := append(sc.args(), "get", "pods", "-o", "json")
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stdout, _, err := traceexec.ExecOutput(ctx, cmd)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var pods corev1.PodList
	if err := json.Unmarshal([]byte(stdout), &pods); err != nil {
		return nil, nil, fmt.Errorf("failed to decode pod list: %w", err)
	}

	var names []string
	var existing *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Name == podName {
			existing = pod
		}
		if strings.HasPrefix(pod.Name, containerNamePrefix) || pod.Name == podName {
			names = append(names, pod.Name)
		}
	}
	return names, existing, nil
}

func (k kubectl) garbageCollectEngines(ctx context.Context, sc scope, cleanup bool, engines []string) {
	if !cleanup || len(engines) == 0 {
		return
	}
	for _, kind := range []string{"pod", "cm", "secret"} {
		args := append(sc.args(), "delete", kind, "--ignore-not-found")
		args = append(args, engines...)
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		if err := traceexec.Exec(ctx, cmd); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}
