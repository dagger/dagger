package imageload

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dagger/dagger/util/traceexec"
	telemetry "github.com/dagger/otel-go"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel"
	"gopkg.in/yaml.v3"
)

type Incus struct{}

func init() {
	register("incus-image", Incus{})
}

func (loader Incus) Loader(ctx context.Context) (*Loader, error) {
	return &Loader{
		TarballWriter: loader.loadTarball,
		TarballReader: loader.saveTarball,
	}, nil
}

func (loader Incus) loadTarball(ctx context.Context, name string, tarball io.Reader) (rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "load "+name)
	defer telemetry.EndWithCause(span, &rerr)

	alias := incusImageAlias(name)
	tarPath, err := os.CreateTemp("", "dagger-incus-load-*.tar")
	if err != nil {
		return err
	}
	defer func() {
		_ = tarPath.Close()
		_ = os.Remove(tarPath.Name())
	}()

	if _, err := io.Copy(tarPath, tarball); err != nil {
		return err
	}
	if err := tarPath.Close(); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "incus", "image", "import", tarPath.Name(), "--alias", alias, "--reuse")
	if err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated()); err == nil {
		return nil
	}

	converted, err := imageArchiveToIncusTarball(tarPath.Name(), alias)
	if err != nil {
		return fmt.Errorf("incus image import failed and conversion failed: %w", err)
	}
	defer os.Remove(converted)

	cmd = exec.CommandContext(ctx, "incus", "image", "import", converted, "--alias", alias, "--reuse")
	if err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated()); err != nil {
		return fmt.Errorf("incus image import failed: %w", err)
	}
	return nil
}

func (loader Incus) saveTarball(ctx context.Context, name string, tarball io.Writer) (rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "save "+name)
	defer telemetry.EndWithCause(span, &rerr)

	alias := incusImageAlias(name)
	outDir, err := os.MkdirTemp("", "dagger-incus-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outDir)

	cmd := exec.CommandContext(ctx, "incus", "image", "export", "local:"+alias, outDir)
	if err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated()); err != nil {
		return fmt.Errorf("incus image export failed: %w", err)
	}

	gw := gzip.NewWriter(tarball)
	tw := tar.NewWriter(gw)
	if err := filepath.Walk(outDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == outDir {
			return nil
		}
		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			hdr, err = tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
		return nil
	}); err != nil {
		_ = tw.Close()
		_ = gw.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return nil
}

func incusImageAlias(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return "dagger-" + hex.EncodeToString(sum[:8])
}

type dockerManifestEntry struct {
	Config   string   `json:"Config"`
	Layers   []string `json:"Layers"`
	RepoTags []string `json:"RepoTags"`
}

type dockerImageConfig struct {
	Architecture string     `json:"architecture"`
	OS           string     `json:"os"`
	Created      *time.Time `json:"created"`
}

type imageArchive struct {
	cfg     dockerImageConfig
	layers  []string
	cleanup func()
}

type archiveIndex struct {
	sourcePath string
	dir        string
	files      map[string]string
}

func imageArchiveToIncusTarball(sourcePath, alias string) (_ string, rerr error) {
	archive, err := parseImageArchive(sourcePath)
	if err != nil {
		return "", err
	}
	if archive.cleanup != nil {
		defer archive.cleanup()
	}

	tempDir, err := os.MkdirTemp("", "dagger-incus-rootfs-*")
	if err != nil {
		return "", err
	}
	rootfsDir := filepath.Join(tempDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)

	for _, layerPath := range archive.layers {
		if err := unpackLayer(rootfsDir, layerPath); err != nil {
			return "", err
		}
	}

	metaPath := filepath.Join(tempDir, "metadata.yaml")
	metadataYAML, err := buildMetadataYAML(alias, archive.cfg)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(metaPath, []byte(metadataYAML), 0o644); err != nil {
		return "", err
	}

	outFile, err := os.CreateTemp("", "dagger-incus-image-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer func() {
		if rerr != nil {
			_ = outFile.Close()
			_ = os.Remove(outFile.Name())
		}
	}()

	gw := gzip.NewWriter(outFile)
	tw := tar.NewWriter(gw)
	if err := writeFileToTar(tw, metaPath, "metadata.yaml"); err != nil {
		return "", err
	}
	if err := filepath.Walk(rootfsDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == rootfsDir {
			return nil
		}
		rel, err := filepath.Rel(rootfsDir, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join("rootfs", rel))
		if info.IsDir() {
			name += "/"
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(p)
			if err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
		return nil
	}); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gw.Close(); err != nil {
		return "", err
	}
	if err := outFile.Close(); err != nil {
		return "", err
	}
	return outFile.Name(), nil
}

func parseImageArchive(sourcePath string) (archive *imageArchive, rerr error) {
	index, err := indexImageArchive(sourcePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil {
			index.close()
		}
	}()

	if archive, err := parseOCIImageArchive(index); err == nil {
		archive.cleanup = index.close
		return archive, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	archive, err = parseDockerImageArchive(index)
	if err != nil {
		return nil, err
	}
	archive.cleanup = index.close
	return archive, nil
}

func parseDockerImageArchive(index *archiveIndex) (*imageArchive, error) {
	var manifestBytes []byte
	if err := readArchiveFile(index, "manifest.json", &manifestBytes); err != nil {
		return nil, err
	}

	var manifests []dockerManifestEntry
	if err := json.Unmarshal(manifestBytes, &manifests); err != nil {
		return nil, err
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf("manifest.json in %s is empty", index.dir)
	}
	manifest, err := selectDockerManifest(index, manifests)
	if err != nil {
		return nil, err
	}

	var configBytes []byte
	if err := readArchiveFile(index, manifest.Config, &configBytes); err != nil {
		return nil, err
	}

	cfg, err := parseDockerImageConfig(configBytes)
	if err != nil {
		return nil, err
	}

	layers := make([]string, 0, len(manifest.Layers))
	for _, layerName := range manifest.Layers {
		layerPath, err := index.file(layerName)
		if err != nil {
			return nil, err
		}
		layers = append(layers, layerPath)
	}

	return &imageArchive{cfg: cfg, layers: layers, cleanup: index.close}, nil
}

func parseOCIImageArchive(index *archiveIndex) (*imageArchive, error) {
	var indexBytes []byte
	if err := readArchiveFile(index, "index.json", &indexBytes); err != nil {
		return nil, err
	}

	var ociIndex ocispecs.Index
	if err := json.Unmarshal(indexBytes, &ociIndex); err != nil {
		return nil, err
	}
	if len(ociIndex.Manifests) == 0 {
		return nil, fmt.Errorf("index.json in %s is empty", index.dir)
	}

	desc, err := selectOCIManifestDescriptor(ociIndex)
	if err != nil {
		return nil, err
	}

	archive, err := parseOCIManifestDescriptor(index, desc)
	if err != nil {
		return nil, err
	}
	return archive, nil
}

func parseOCIManifestDescriptor(index *archiveIndex, desc ocispecs.Descriptor) (*imageArchive, error) {
	var manifestBytes []byte
	if err := readArchiveFile(index, filepath.Join("blobs", "sha256", desc.Digest.Encoded()), &manifestBytes); err != nil {
		return nil, err
	}

	var manifest ocispecs.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err == nil && manifest.Config.Digest.String() != "" {
		return parseOCIManifest(index, manifest)
	}

	var nestedIndex ocispecs.Index
	if err := json.Unmarshal(manifestBytes, &nestedIndex); err == nil {
		if len(nestedIndex.Manifests) == 0 {
			return nil, fmt.Errorf("nested index in %s is empty", index.dir)
		}
		desc, err := selectOCIManifestDescriptor(nestedIndex)
		if err != nil {
			return nil, err
		}
		return parseOCIManifestDescriptor(index, desc)
	}

	return nil, fmt.Errorf("unsupported OCI manifest blob %s in %s", desc.Digest.String(), index.dir)
}

func parseOCIManifest(index *archiveIndex, manifest ocispecs.Manifest) (*imageArchive, error) {
	var configBytes []byte
	if err := readArchiveFile(index, filepath.Join("blobs", "sha256", manifest.Config.Digest.Encoded()), &configBytes); err != nil {
		return nil, err
	}

	cfg, err := parseDockerImageConfig(configBytes)
	if err != nil {
		return nil, err
	}

	layers := make([]string, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		layerPath, err := index.file(filepath.Join("blobs", "sha256", layer.Digest.Encoded()))
		if err != nil {
			return nil, err
		}
		layers = append(layers, layerPath)
	}

	return &imageArchive{cfg: cfg, layers: layers}, nil
}

func parseDockerImageConfig(configBytes []byte) (dockerImageConfig, error) {
	var cfg dockerImageConfig
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return dockerImageConfig{}, err
	}
	return cfg, nil
}

func readArchiveFile(index *archiveIndex, target string, dst *[]byte) error {
	filePath, err := index.file(target)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	*dst = b
	return nil
}

func unpackLayer(rootfs string, layerPath string) error {
	f, err := os.Open(layerPath)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 2)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if n == 2 && buf[0] == 0x1f && buf[1] == 0x8b {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		gr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gr.Close()
		return untarInto(rootfs, gr)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return untarInto(rootfs, f)
}

func untarInto(rootfs string, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := safeExtractPath(rootfs, hdr.Name)
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		dir := filepath.Dir(target)

		switch {
		case strings.HasPrefix(base, ".wh.") && base != ".wh..wh..opq":
			whTarget, err := safeArchiveTarget(rootfs, filepath.Join(filepath.Dir(hdr.Name), strings.TrimPrefix(base, ".wh.")))
			if err != nil {
				return err
			}
			resolvedWhDir, err := resolvePathWithinRoot(rootfs, filepath.Dir(whTarget), false)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if resolvedWhDir != "" {
				whTarget = filepath.Join(resolvedWhDir, filepath.Base(whTarget))
			}
			if err := os.RemoveAll(whTarget); err != nil {
				return err
			}
			continue
		case base == ".wh..wh..opq":
			entries, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
					return err
				}
			}
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			resolvedDir, err := resolvePathWithinRoot(rootfs, target, true)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(resolvedDir, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			resolvedDir, err := resolvePathWithinRoot(rootfs, dir, true)
			if err != nil {
				return err
			}
			if err := safeSymlinkTarget(rootfs, resolvedDir, hdr.Linkname); err != nil {
				return err
			}
			linkTarget := filepath.Join(resolvedDir, filepath.Base(target))
			if err := os.RemoveAll(linkTarget); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, linkTarget); err != nil {
				return err
			}
		case tar.TypeLink:
			resolvedDir, err := resolvePathWithinRoot(rootfs, dir, true)
			if err != nil {
				return err
			}
			linkTarget, err := safeResolvedPath(rootfs, dir, hdr.Linkname)
			if err != nil {
				return err
			}
			targetPath := filepath.Join(resolvedDir, filepath.Base(target))
			if err := os.RemoveAll(targetPath); err != nil {
				return err
			}
			if err := os.Link(linkTarget, targetPath); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			resolvedDir, err := resolvePathWithinRoot(rootfs, dir, true)
			if err != nil {
				return err
			}
			filePath := filepath.Join(resolvedDir, filepath.Base(target))
			f, err := safeOpenFile(rootfs, filePath, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			continue
		}
	}
}

func buildMetadataYAML(alias string, cfg dockerImageConfig) (string, error) {
	arch := normalizeIncusArchitecture(cfg.Architecture)
	if arch == "" {
		arch = normalizeIncusArchitecture(runtime.GOARCH)
	}
	osName := cfg.OS
	if osName == "" {
		osName = runtime.GOOS
	}
	created := time.Now().Unix()
	if cfg.Created != nil {
		created = cfg.Created.Unix()
	}

	metadata := map[string]any{
		"architecture":  arch,
		"creation_date": created,
		"properties": map[string]string{
			"description": alias,
			"os":          osName,
		},
	}
	b, err := yaml.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func normalizeIncusArchitecture(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	case "arm":
		return "armhf"
	default:
		return arch
	}
}

func selectDockerManifest(index *archiveIndex, manifests []dockerManifestEntry) (dockerManifestEntry, error) {
	for _, manifest := range manifests {
		var configBytes []byte
		if err := readArchiveFile(index, manifest.Config, &configBytes); err != nil {
			continue
		}
		if _, err := parseDockerImageConfig(configBytes); err != nil {
			continue
		}
		return manifest, nil
	}

	return dockerManifestEntry{}, fmt.Errorf("no usable manifest in %s", index.dir)
}

func selectOCIManifestDescriptor(index ocispecs.Index) (ocispecs.Descriptor, error) {
	if len(index.Manifests) == 0 {
		return ocispecs.Descriptor{}, fmt.Errorf("no OCI manifest in index")
	}
	return index.Manifests[0], nil
}

func indexImageArchive(sourcePath string) (*archiveIndex, error) {
	dir, err := os.MkdirTemp("", "dagger-incus-archive-*")
	if err != nil {
		return nil, err
	}

	index := &archiveIndex{
		sourcePath: sourcePath,
		dir:        dir,
		files:      map[string]string{},
	}

	return index, nil
}

func (a *archiveIndex) close() {
	if a == nil || a.dir == "" {
		return
	}
	_ = os.RemoveAll(a.dir)
}

func (a *archiveIndex) file(target string) (string, error) {
	if a == nil {
		return "", os.ErrNotExist
	}
	for _, key := range []string{target, normalizeArchiveName(target)} {
		if key == "" {
			continue
		}
		if p, ok := a.files[key]; ok {
			return p, nil
		}
	}
	path, err := a.materialize(target)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (a *archiveIndex) materialize(target string) (string, error) {
	normalized := normalizeArchiveName(target)
	if normalized == "" {
		return "", fmt.Errorf("file %q not found in archive: %w", target, os.ErrNotExist)
	}
	outPath := filepath.Join(a.dir, fmt.Sprintf("%x", sha256.Sum256([]byte(normalized))))
	if err := extractArchiveFile(a.sourcePath, target, outPath); err != nil {
		return "", err
	}
	for _, key := range []string{target, normalized} {
		if key == "" {
			continue
		}
		a.files[key] = outPath
	}
	return outPath, nil
}

func extractArchiveFile(sourcePath, target, outPath string) (rerr error) {
	scan, err := openArchiveScan(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := scan.Close(); rerr == nil && cerr != nil {
			rerr = cerr
		}
	}()

	normalizedTarget := normalizeArchiveName(target)
	if normalizedTarget == "" {
		return fmt.Errorf("file %q not found in archive: %w", target, os.ErrNotExist)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		if rerr != nil {
			_ = out.Close()
			_ = os.Remove(outPath)
		}
	}()

	tr := tar.NewReader(scan)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		if !archiveEntryMatches(hdr.Name, target, normalizedTarget) {
			continue
		}
		found = true
		if err := out.Truncate(0); err != nil {
			return err
		}
		if _, err := out.Seek(0, io.SeekStart); err != nil {
			return err
		}
		written, err := io.Copy(out, tr)
		if err != nil {
			return err
		}
		if err := out.Truncate(written); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("file %q not found in archive: %w", target, os.ErrNotExist)
	}
	return out.Close()
}

func archiveEntryMatches(hdrName, target, normalizedTarget string) bool {
	if hdrName == target || hdrName == normalizedTarget {
		return true
	}
	normalized := normalizeArchiveName(hdrName)
	return normalized != "" && normalized == normalizedTarget
}

type archiveScan struct {
	reader  io.Reader
	closers []io.Closer
}

func (a *archiveScan) Read(p []byte) (int, error) {
	return a.reader.Read(p)
}

func (a *archiveScan) Close() error {
	var firstErr error
	for _, closer := range a.closers {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func openArchiveScan(sourcePath string) (*archiveScan, error) {
	f, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}

	magic := make([]byte, 2)
	n, err := io.ReadFull(f, magic)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}

	scan := &archiveScan{reader: f, closers: []io.Closer{f}}
	if n == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gr, err := gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		scan.reader = gr
		scan.closers = append([]io.Closer{gr}, scan.closers...)
	}
	return scan, nil
}

func normalizeArchiveName(name string) string {
	name = strings.TrimSpace(strings.TrimPrefix(name, "./"))
	name = strings.TrimPrefix(name, "/")
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return ""
		}
	}
	name = path.Clean(name)
	if name == "." || name == "" {
		return ""
	}
	return name
}

func safeExtractPath(rootfs, name string) (string, error) {
	cleaned := normalizeArchiveName(name)
	if cleaned == "" {
		return "", fmt.Errorf("illegal file path: %q", name)
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("illegal file path: %q", name)
	}
	target := filepath.Join(rootfs, filepath.FromSlash(cleaned))
	rel, err := filepath.Rel(rootfs, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("illegal file path: %q", name)
	}
	return target, nil
}

func safeArchiveTarget(rootfs, name string) (string, error) {
	target, err := safeExtractPath(rootfs, name)
	if err != nil {
		return "", err
	}
	return target, nil
}

func resolvePathWithinRoot(rootfs, target string, create bool) (string, error) {
	rel, err := filepath.Rel(rootfs, target)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return rootfs, nil
	}

	curr := rootfs
	parts := splitPath(rel)
	symlinkHops := 0
	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(curr, part)
		info, err := os.Lstat(next)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				symlinkHops++
				if symlinkHops > 255 {
					return "", fmt.Errorf("too many symlink hops while resolving %q", target)
				}
				linkname, err := os.Readlink(next)
				if err != nil {
					return "", err
				}
				resolved, err := archiveTargetWithinRoot(rootfs, linkname)
				if err != nil {
					return "", err
				}
				resolvedRel, err := filepath.Rel(rootfs, resolved)
				if err != nil {
					return "", err
				}
				if err := ensurePathWithinRoot(rootfs, resolved); err != nil {
					return "", err
				}
				parts = append(splitPath(resolvedRel), parts...)
				curr = rootfs
				continue
			}
			if len(parts) > 0 && !info.IsDir() {
				return "", fmt.Errorf("path component %q is not a directory", next)
			}
			curr = next
		case errors.Is(err, os.ErrNotExist):
			if !create {
				return "", os.ErrNotExist
			}
			if err := os.Mkdir(next, 0o755); err != nil && !os.IsExist(err) {
				return "", err
			}
			curr = next
		default:
			return "", err
		}
	}
	return curr, nil
}

func safeResolvedPath(rootfs, baseDir, name string) (string, error) {
	_ = baseDir
	target, err := archiveTargetWithinRoot(rootfs, name)
	if err != nil {
		return "", err
	}
	resolved, err := resolvePathWithinRoot(rootfs, target, false)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func safeSymlinkTarget(rootfs, baseDir, linkname string) error {
	_ = rootfs
	_ = baseDir
	_ = linkname
	return nil
}

func safeOpenFile(rootfs, target string, perm os.FileMode) (*os.File, error) {
	dir, err := resolvePathWithinRoot(rootfs, filepath.Dir(target), true)
	if err != nil {
		return nil, err
	}
	full := filepath.Join(dir, filepath.Base(target))
	if _, err := os.Lstat(full); err == nil {
		if err := os.RemoveAll(full); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(full, os.O_CREATE|os.O_RDWR|os.O_TRUNC, perm)
}

func ensurePathWithinRoot(rootfs, target string) error {
	rel, err := filepath.Rel(rootfs, target)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("illegal file path: %s", target)
	}
	return nil
}

func archiveTargetWithinRoot(rootfs, name string) (string, error) {
	cleaned := filepath.Clean(string(os.PathSeparator) + strings.TrimPrefix(name, string(os.PathSeparator)))
	target := filepath.Join(rootfs, strings.TrimPrefix(cleaned, string(os.PathSeparator)))
	if err := ensurePathWithinRoot(rootfs, target); err != nil {
		return "", fmt.Errorf("illegal file path: %q", name)
	}
	return target, nil
}

func splitPath(p string) []string {
	if p == "" || p == "." {
		return nil
	}
	return strings.Split(p, string(os.PathSeparator))
}

func hasParentTraversal(name string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(name, "./"))
	trimmed = strings.TrimPrefix(trimmed, "/")
	for _, part := range strings.Split(trimmed, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func writeFileToTar(tw *tar.Writer, sourcePath, targetName string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = targetName
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}
