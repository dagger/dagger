package drivers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/dagger/dagger/engine/distconsts"
)

// specHashAnnotation records a hash of the spec a pod was created from, so a
// pod created with different resources, volume, image, etc. is not reused.
const specHashAnnotation = "dagger.io/engine-spec-hash"

const gpuResource corev1.ResourceName = "nvidia.com/gpu"

// podConfig is the pod configuration that the runner URL's query selects.
type podConfig struct {
	Resources corev1.ResourceRequirements
	Volume    corev1.VolumeSource
}

// parsePodConfig reads the pod configuration from the runner URL's query:
//
//   - requests=NAME=QUANTITY[,NAME=QUANTITY...] sets resource requests,
//     e.g. requests=cpu=2,memory=4Gi. May be repeated.
//   - limits=NAME=QUANTITY[,NAME=QUANTITY...] sets resource limits,
//     e.g. limits=memory=8Gi. May be repeated.
//   - volume=SOURCE sets the volume holding the engine's state:
//     emptyDir (the default), emptyDir:SIZE_LIMIT, pvc:CLAIM_NAME,
//     hostPath:PATH, or a JSON-encoded Kubernetes VolumeSource.
func parsePodConfig(q url.Values) (*podConfig, error) {
	var cfg podConfig
	var err error
	if cfg.Resources.Requests, err = parseResourceList(q["requests"]); err != nil {
		return nil, fmt.Errorf("invalid requests: %w", err)
	}
	if cfg.Resources.Limits, err = parseResourceList(q["limits"]); err != nil {
		return nil, fmt.Errorf("invalid limits: %w", err)
	}
	if cfg.Volume, err = parseVolumeSource(q.Get("volume")); err != nil {
		return nil, fmt.Errorf("invalid volume: %w", err)
	}
	return &cfg, nil
}

func parseResourceList(values []string) (corev1.ResourceList, error) {
	var list corev1.ResourceList
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			if item == "" {
				continue
			}
			name, quantity, ok := strings.Cut(item, "=")
			if !ok || name == "" {
				return nil, fmt.Errorf("%q is not NAME=QUANTITY", item)
			}
			q, err := resource.ParseQuantity(quantity)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if list == nil {
				list = corev1.ResourceList{}
			}
			list[corev1.ResourceName(name)] = q
		}
	}
	return list, nil
}

func parseVolumeSource(value string) (corev1.VolumeSource, error) {
	if strings.HasPrefix(value, "{") {
		var src corev1.VolumeSource
		dec := json.NewDecoder(strings.NewReader(value))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&src); err != nil {
			return src, err
		}
		return src, nil
	}

	kind, arg, _ := strings.Cut(value, ":")
	switch kind {
	case "", "emptyDir":
		src := corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}
		if arg != "" {
			size, err := resource.ParseQuantity(arg)
			if err != nil {
				return src, fmt.Errorf("emptyDir size limit: %w", err)
			}
			src.EmptyDir.SizeLimit = &size
		}
		return src, nil
	case "pvc":
		if arg == "" {
			return corev1.VolumeSource{}, fmt.Errorf("pvc needs a claim name, as pvc:NAME")
		}
		return corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: arg}}, nil
	case "hostPath":
		if arg == "" {
			return corev1.VolumeSource{}, fmt.Errorf("hostPath needs a path, as hostPath:PATH")
		}
		return corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
			Path: arg,
			Type: new(corev1.HostPathDirectoryOrCreate),
		}}, nil
	default:
		return corev1.VolumeSource{}, fmt.Errorf("unknown volume type %q: want emptyDir, pvc, hostPath, or a JSON VolumeSource", kind)
	}
}

// resources returns the container's resource requirements, adding a GPU when
// GPU support is enabled. Kubernetes requires a limit for extended resources
// such as GPUs, and defaults the request to it.
func (cfg *podConfig) resources(gpu bool) corev1.ResourceRequirements {
	res := *cfg.Resources.DeepCopy()
	if gpu {
		if _, ok := res.Limits[gpuResource]; !ok {
			if res.Limits == nil {
				res.Limits = corev1.ResourceList{}
			}
			res.Limits[gpuResource] = resource.MustParse("1")
		}
	}
	return res
}

const (
	podNameEnv      = "POD_NAME"
	podNamespaceEnv = "POD_NAMESPACE"
)

// downwardEnv exposes the pod's name and namespace through the Downward API,
// for subPathExpr among others. They are resolved in the pod rather than
// taken from the runner URL, which may leave the namespace to the
// kubeconfig's default.
var downwardEnv = []corev1.EnvVar{
	{
		Name: podNameEnv,
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
		},
	},
	{
		Name: podNamespaceEnv,
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		},
	},
}

// stateVolumeMount mounts the state volume as the engine's state directory.
// It relies on the container having downwardEnv.
//
// A volume that can outlive the pod or be shared between pods is mounted from
// a subdirectory keyed by pod name, and so by engine version, so that engines
// of different versions, which overlap during an upgrade, don't contend for
// the same state directory and its lock. A hostPath is node-wide, so it is
// keyed by namespace too, as $POD_NAMESPACE/$POD_NAME. A volume that belongs
// to a single pod is mounted whole.
func stateVolumeMount(volume corev1.Volume) corev1.VolumeMount {
	mount := corev1.VolumeMount{
		Name:      volume.Name,
		MountPath: distconsts.EngineDefaultStateDir,
	}
	switch {
	case volume.EmptyDir != nil, volume.Ephemeral != nil:
	case volume.HostPath != nil:
		mount.SubPathExpr = "$(" + podNamespaceEnv + ")/$(" + podNameEnv + ")"
	default:
		mount.SubPathExpr = "$(" + podNameEnv + ")"
	}
	return mount
}

// specHash returns a short, stable hash of a pod spec.
func specHash(spec corev1.PodSpec) (string, error) {
	b, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8]), nil
}

// podReady reports whether the pod is running and ready.
func podReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// unusableReason reports why an existing engine pod cannot be reused, or ""
// if it can, possibly after it becomes ready.
func unusableReason(pod *corev1.Pod, wantHash string) string {
	switch {
	case pod.DeletionTimestamp != nil:
		return "it is being deleted"
	case pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded:
		return fmt.Sprintf("it has exited (phase %s)", pod.Status.Phase)
	case pod.Annotations[specHashAnnotation] != wantHash:
		return "its spec differs from the requested one"
	default:
		return ""
	}
}
