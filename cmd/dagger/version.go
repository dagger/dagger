package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/distconsts"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/muesli/termenv"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/engine"
)

var (
	forceVersionCheck bool
)

func versionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print dagger version",
		// Disable version hook here to avoid double version check
		PersistentPreRun: func(*cobra.Command, []string) {},
		Args:             cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), long())
			if forceVersionCheck {
				updateAvailable, err := updateAvailable(cmd.Context())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "\nFailed to check for updates: %v\n", err)
					os.Exit(1)
					return
				}
				if updateAvailable != "" {
					versionNag(cmd.ErrOrStderr(), updateAvailable)
				}
			}
		},
	}
	cmd.Flags().BoolVar(&forceVersionCheck, "check", false, "Check for updates")
	return cmd
}

func short() string {
	return fmt.Sprintf("dagger %s (%s:%s)", engine.Version, engine.EngineImageRepo, engine.Tag)
}

func long() string {
	return fmt.Sprintf("%s %s/%s", short(), runtime.GOOS, runtime.GOARCH)
}

func updateAvailable(ctx context.Context) (string, error) {
	if engine.IsDevVersion(engine.Version) {
		return "", nil
	}

	latest, err := latestVersion(ctx)
	if err != nil {
		return "", err
	}

	// Update is available
	if semver.Compare(engine.Version, latest) < 0 {
		return latest, nil
	}

	// We're already up to date, so there isn't a new version available
	return "", nil
}

func latestVersion(ctx context.Context) (v string, rerr error) {
	imageRef := fmt.Sprintf("%s:latest", engine.EngineImageRepo)

	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", errors.Wrap(err, "parsing image reference")
	}

	desc, err := remote.Get(ref,
		remote.WithContext(ctx),
		// The default auth keychain parses the same docker credentials as used by the buildkit
		// session attachable.
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithUserAgent(enginetel.Labels{}.WithCILabels().WithAnonymousGitLabels(workdir).UserAgent()),
	)
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve image")
	}

	annotations, err := manifestAnnotations(desc)
	if err != nil {
		return "", errors.Wrap(err, "failed to get annotations")
	}

	version, ok := annotations[distconsts.OCIVersionAnnotation]
	if !ok {
		return "", errors.New("no version found in annotations")
	}

	return version, nil
}

func manifestAnnotations(desc *remote.Descriptor) (map[string]string, error) {
	annotations := make(map[string]string)

	switch desc.MediaType {
	case types.OCIImageIndex, types.DockerManifestList:
		// Handle an OCI image index (v1.IndexManifest)
		var index v1.IndexManifest

		err := json.Unmarshal(desc.Manifest, &index)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling index: %w", err)
		}

		// Merge annotations at the index (top-level) if they exist
		if index.Annotations != nil {
			for key, value := range index.Annotations {
				annotations[key] = value
			}
		}

		for _, manifest := range index.Manifests {
			if manifest.Annotations != nil {
				for key, value := range manifest.Annotations {
					annotations[key] = value
				}
			}
		}

	case types.OCIManifestSchema1, types.DockerManifestSchema2:
		// Handle a single image manifest (v1.Manifest)
		var manifest v1.Manifest

		err := json.Unmarshal(desc.Manifest, &manifest)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling manifest: %w", err)
		}

		// Copy annotations into the map
		if manifest.Annotations != nil {
			for key, value := range manifest.Annotations {
				annotations[key] = value
			}
		}

	default:
		return nil, fmt.Errorf("unsupported media type: %s", desc.MediaType)
	}

	return annotations, nil
}

func versionNag(w io.Writer, latest string) {
	output := idtui.NewOutput(w)

	fmt.Fprint(w, "\r\n"+
		output.String("A new release of dagger is available: ").Foreground(termenv.ANSIYellow).String()+
		output.String(engine.Version).Foreground(termenv.ANSICyan).String()+
		" → "+
		output.String(latest).Foreground(termenv.ANSICyan).String()+
		"\n"+

		"To upgrade, see https://docs.dagger.io/install\n"+
		output.String("https://github.com/dagger/dagger/releases/tag/"+latest).Foreground(termenv.ANSIYellow).String()+
		"\n",
	)
}
