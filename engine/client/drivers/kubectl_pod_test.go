package drivers

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dagger/dagger/engine/distconsts"
)

func TestParsePodConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := parsePodConfig(url.Values{})
		require.NoError(t, err)
		require.Empty(t, cfg.Resources.Requests)
		require.Empty(t, cfg.Resources.Limits)
		require.NotNil(t, cfg.Volume.EmptyDir)
		require.Nil(t, cfg.Volume.EmptyDir.SizeLimit)
	})

	t.Run("resources", func(t *testing.T) {
		u := parse(t, "image+kubectl://engine?requests=cpu=2,memory=4Gi&limits=memory=8Gi&limits=ephemeral-storage=50Gi")
		cfg, err := parsePodConfig(u.Query())
		require.NoError(t, err)
		require.True(t, cfg.Resources.Requests.Cpu().Equal(resource.MustParse("2")))
		require.True(t, cfg.Resources.Requests.Memory().Equal(resource.MustParse("4Gi")))
		require.True(t, cfg.Resources.Limits.Memory().Equal(resource.MustParse("8Gi")))
		require.True(t, cfg.Resources.Limits.StorageEphemeral().Equal(resource.MustParse("50Gi")))
	})

	t.Run("invalid resources", func(t *testing.T) {
		_, err := parsePodConfig(url.Values{"limits": {"memory"}})
		require.ErrorContains(t, err, "NAME=QUANTITY")
		_, err = parsePodConfig(url.Values{"requests": {"cpu=lots"}})
		require.ErrorContains(t, err, "invalid requests")
	})

	for _, tc := range []struct {
		volume string
		check  func(t *testing.T, src corev1.VolumeSource)
	}{
		{"emptyDir", func(t *testing.T, src corev1.VolumeSource) {
			require.NotNil(t, src.EmptyDir)
		}},
		{"emptyDir:20Gi", func(t *testing.T, src corev1.VolumeSource) {
			require.True(t, src.EmptyDir.SizeLimit.Equal(resource.MustParse("20Gi")))
		}},
		{"pvc:dagger-cache", func(t *testing.T, src corev1.VolumeSource) {
			require.Equal(t, "dagger-cache", src.PersistentVolumeClaim.ClaimName)
		}},
		{"hostPath:/var/lib/dagger", func(t *testing.T, src corev1.VolumeSource) {
			require.Equal(t, "/var/lib/dagger", src.HostPath.Path)
		}},
		{`{"emptyDir":{"medium":"Memory"}}`, func(t *testing.T, src corev1.VolumeSource) {
			require.Equal(t, corev1.StorageMediumMemory, src.EmptyDir.Medium)
		}},
	} {
		t.Run("volume "+tc.volume, func(t *testing.T) {
			cfg, err := parsePodConfig(url.Values{"volume": {tc.volume}})
			require.NoError(t, err)
			tc.check(t, cfg.Volume)
		})
	}

	for _, bad := range []string{"pvc", "pvc:", "hostPath:", "nfs:server", "emptyDir:huge", `{"nope":{}}`} {
		t.Run("invalid volume "+bad, func(t *testing.T) {
			_, err := parsePodConfig(url.Values{"volume": {bad}})
			require.ErrorContains(t, err, "invalid volume")
		})
	}
}

func TestPodConfigGPU(t *testing.T) {
	cfg, err := parsePodConfig(url.Values{"limits": {"memory=1Gi"}})
	require.NoError(t, err)

	res := cfg.resources(true)
	require.True(t, res.Limits[gpuResource].Equal(resource.MustParse("1")))
	require.True(t, res.Limits.Memory().Equal(resource.MustParse("1Gi")))
	require.NotContains(t, cfg.Resources.Limits, gpuResource, "must not modify the config")

	require.NotContains(t, cfg.resources(false).Limits, gpuResource)
}

func TestUnusableReason(t *testing.T) {
	pod := func(phase corev1.PodPhase, hash string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{specHashAnnotation: hash}},
			Status:     corev1.PodStatus{Phase: phase},
		}
	}
	require.Empty(t, unusableReason(pod(corev1.PodRunning, "h"), "h"))
	require.Empty(t, unusableReason(pod(corev1.PodPending, "h"), "h"))
	require.Contains(t, unusableReason(pod(corev1.PodFailed, "h"), "h"), "exited")
	require.Contains(t, unusableReason(pod(corev1.PodSucceeded, "h"), "h"), "exited")
	require.Contains(t, unusableReason(pod(corev1.PodRunning, "old"), "h"), "spec differs")

	deleting := pod(corev1.PodRunning, "h")
	deleting.DeletionTimestamp = &metav1.Time{}
	require.Contains(t, unusableReason(deleting, "h"), "being deleted")
}

func TestEngineObjectsSpecHash(t *testing.T) {
	k := kubectlImageDriver
	image := &imageTarget{Ref: "registry.example/dagger:v1", Name: "engine"}
	hash := func(q url.Values) string {
		cfg, err := parsePodConfig(q)
		require.NoError(t, err)
		objs, err := k.engineObjects("dagger", "engine", image, cfg, &DriverOpts{})
		require.NoError(t, err)
		require.Equal(t, corev1.RestartPolicyAlways, objs.pod.Spec.RestartPolicy)
		return objs.pod.Annotations[specHashAnnotation]
	}

	base := hash(url.Values{})
	require.NotEmpty(t, base)
	require.Equal(t, base, hash(url.Values{}), "hash must be stable")
	require.NotEqual(t, base, hash(url.Values{"limits": {"memory=8Gi"}}))
	require.NotEqual(t, base, hash(url.Values{"volume": {"pvc:cache"}}))
}

func TestEngineObjectsStateSubPath(t *testing.T) {
	for _, tc := range []struct {
		volume      string
		subPathExpr string
	}{
		{"", ""},
		{"emptyDir:10Gi", ""},
		{`{"ephemeral":{"volumeClaimTemplate":{"spec":{}}}}`, ""},
		{"hostPath:/srv/dagger", "$(POD_NAMESPACE)/$(POD_NAME)"},
		{`{"hostPath":{"path":"/var/lib/dagger"}}`, "$(POD_NAMESPACE)/$(POD_NAME)"},
		{"pvc:cache", "$(POD_NAME)"},
		{`{"nfs":{"server":"nfs","path":"/dagger"}}`, "$(POD_NAME)"},
	} {
		t.Run(tc.volume, func(t *testing.T) {
			cfg, err := parsePodConfig(url.Values{"volume": {tc.volume}})
			require.NoError(t, err)
			image := &imageTarget{Ref: "registry.example/dagger:v1", Name: "dagger-engine-v1"}
			objs, err := kubectlImageDriver.engineObjects("dagger", image.Name, image, cfg, &DriverOpts{})
			require.NoError(t, err)
			mount := objs.pod.Spec.Containers[0].VolumeMounts[0]
			require.Equal(t, distconsts.EngineDefaultStateDir, mount.MountPath)
			require.Empty(t, mount.SubPath)
			require.Equal(t, tc.subPathExpr, mount.SubPathExpr)

			env := objs.pod.Spec.Containers[0].Env
			for _, want := range downwardEnv {
				require.Contains(t, env, want)
			}
		})
	}
}

// fakeProvisionKubectl puts a kubectl on PATH that lists pods as the given
// pods, succeeds at everything else, and records its arguments in the
// returned file, one invocation per line.
func fakeProvisionKubectl(t *testing.T, pods ...corev1.Pod) (argsLog string) {
	t.Helper()

	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	podsJSON := filepath.Join(dir, "pods.json")

	list, err := json.Marshal(corev1.PodList{Items: pods})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(podsJSON, list, 0o600))

	script := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\n" +
		`case "$*" in *"get pods -o json"*) cat ` + podsJSON + `;; *"apply -f-"*) cat > /dev/null;; esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsLog
}

func TestKubectlProvisionReuse(t *testing.T) {
	t.Setenv("DAGGER_LEAVE_OLD_ENGINE", "false")
	const rawURL = "image+kubectl://registry.example/dagger:v1?pod=engine&namespace=dagger&limits=memory=8Gi"
	u := parse(t, rawURL)

	cfg, err := parsePodConfig(u.Query())
	require.NoError(t, err)
	image, err := parseImageTarget(u, "pod")
	require.NoError(t, err)
	objs, err := kubectlImageDriver.engineObjects("dagger", "engine", image, cfg, &DriverOpts{})
	require.NoError(t, err)
	wantHash := objs.pod.Annotations[specHashAnnotation]

	enginePod := func(phase corev1.PodPhase, ready bool, hash string) corev1.Pod {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "engine",
				Annotations: map[string]string{specHashAnnotation: hash},
			},
			Status: corev1.PodStatus{
				Phase:      phase,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
			},
		}
	}

	provision := func(t *testing.T, pods ...corev1.Pod) []string {
		argsLog := fakeProvisionKubectl(t, pods...)
		conn, err := kubectlImageDriver.Provision(t.Context(), parse(t, rawURL), &DriverOpts{})
		require.NoError(t, err)
		require.NotNil(t, conn)
		args, err := os.ReadFile(argsLog)
		require.NoError(t, err)
		return strings.Split(strings.TrimSpace(string(args)), "\n")
	}
	ran := func(calls []string, sub string) bool {
		for _, call := range calls {
			if strings.Contains(call, sub) {
				return true
			}
		}
		return false
	}

	t.Run("no pod creates one", func(t *testing.T) {
		calls := provision(t)
		require.True(t, ran(calls, "apply -f-"))
		require.True(t, ran(calls, "wait --for=condition=ready pod engine"))
		require.False(t, ran(calls, "delete"))
	})

	t.Run("healthy pod is reused", func(t *testing.T) {
		calls := provision(t, enginePod(corev1.PodRunning, true, wantHash))
		require.False(t, ran(calls, "apply -f-"))
		require.False(t, ran(calls, "delete"))
	})

	t.Run("failed pod is replaced", func(t *testing.T) {
		calls := provision(t, enginePod(corev1.PodFailed, false, wantHash))
		require.True(t, ran(calls, "delete pod engine --ignore-not-found --wait"))
		require.True(t, ran(calls, "apply -f-"))
	})

	t.Run("pod with a different spec is replaced", func(t *testing.T) {
		calls := provision(t, enginePod(corev1.PodRunning, true, "stale"))
		require.True(t, ran(calls, "delete pod engine --ignore-not-found --wait"))
		require.True(t, ran(calls, "apply -f-"))
	})

	t.Run("unready pod is waited for, then reused", func(t *testing.T) {
		calls := provision(t, enginePod(corev1.PodRunning, false, wantHash))
		require.True(t, ran(calls, "wait --for=condition=ready pod engine"))
		require.False(t, ran(calls, "apply -f-"))
		require.False(t, ran(calls, "delete"))
	})

	t.Run("old engines are collected", func(t *testing.T) {
		old := enginePod(corev1.PodRunning, true, "x")
		old.Name = containerNamePrefix + "old"
		calls := provision(t, enginePod(corev1.PodRunning, true, wantHash), old)
		require.True(t, ran(calls, "delete pod --ignore-not-found "+old.Name))
		require.False(t, ran(calls, "delete pod --ignore-not-found engine"))
	})
}
