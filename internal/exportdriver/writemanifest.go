package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// writeManifestDir writes an archives directory for the packages the
// patterns need, with their tests, from `go list -export`: the layout a
// per-package builder provides, for tests and comparisons. It has every
// package go list compiles, test variants included, but the test mains, and
// a copy of cgo's output for each cgo package.
func writeManifestDir(ctx context.Context, out string, args []string) error {
	var flags, patterns []string
	for i, a := range args {
		if a == "--" {
			flags, patterns = args[:i], args[i+1:]
			break
		}
	}
	if patterns == nil {
		return fmt.Errorf("usage: export-driver --write-manifest=DIR [build flags] -- patterns")
	}
	env, err := goEnv(ctx, os.Environ())
	if err != nil {
		return err
	}
	goflags, err := quotedSplit(env["GOFLAGS"])
	if err != nil {
		return fmt.Errorf("GOFLAGS: %w", err)
	}
	listArgs := append([]string{"list", "-e", "-export", "-compiled", "-deps", "-test",
		"-json=ImportPath,ForTest,Dir,Module,Export,GoFiles,CgoFiles,CompiledGoFiles,EmbedFiles,CFiles,CXXFiles,HFiles,SFiles,MFiles,FFiles,SwigFiles,SwigCXXFiles,SysoFiles"},
		flags...)
	listArgs = append(listArgs, "--")
	listArgs = append(listArgs, patterns...)
	cmd := exec.CommandContext(ctx, "go", listArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go list: %w: %s", err, stderr.String())
	}
	if err := os.MkdirAll(filepath.Join(out, "archives"), 0o755); err != nil {
		return err
	}
	m := manifest{
		Version:    manifestVersion,
		GoVersion:  env["GOVERSION"],
		GOOS:       env["GOOS"],
		GOARCH:     env["GOARCH"],
		CgoEnabled: env["CGO_ENABLED"] == "1",
		// Flags on the command line override GOFLAGS', so they come last.
		BuildTags: nonNil(buildTags(append(goflags, flags...))),
		Packages:  map[string]manifestEntry{},
	}
	if m.CgoEnabled {
		m.Cgo = cgoSettings(ctx, env)
	}
	dec := json.NewDecoder(&stdout)
	for {
		var p struct {
			ImportPath, ForTest, Dir, Export                 string
			Module                                           *packages.Module
			GoFiles, CgoFiles, CompiledGoFiles, EmbedFiles   []string
			CFiles, CXXFiles, HFiles, SFiles, MFiles, FFiles []string
			SwigFiles, SwigCXXFiles, SysoFiles               []string
		}
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if p.Export == "" || strings.HasSuffix(p.ImportPath, ".test") {
			continue
		}
		abs := func(groups ...[]string) []string {
			var out []string
			for _, g := range groups {
				for _, f := range g {
					if !filepath.IsAbs(f) {
						f = filepath.Join(p.Dir, f)
					}
					out = append(out, f)
				}
			}
			return out
		}
		// The same files go/packages reports, so the driver's digest matches.
		digest, err := sourcesDigest(packageFiles{
			Dir:             p.Dir,
			GoFiles:         abs(p.GoFiles, p.CgoFiles),
			OtherFiles:      abs(p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles),
			EmbedFiles:      abs(p.EmbedFiles),
			CompiledGoFiles: abs(p.CompiledGoFiles),
		})
		if err != nil {
			return err
		}
		name := sha256.Sum256([]byte(p.ImportPath))
		path, _, _ := strings.Cut(p.ImportPath, " [")
		rel := filepath.Join("archives", hex.EncodeToString(name[:8])+"-"+filepath.Base(path)+".a")
		if err := copyFile(p.Export, filepath.Join(out, rel)); err != nil {
			return err
		}
		entry := manifestEntry{Archive: filepath.ToSlash(rel), Sources: digest}
		if len(p.CgoFiles) > 0 {
			entry.CgoFiles = p.CgoFiles
			// A module version names the files only from the module cache.
			if rel, err := filepath.Rel(env["GOMODCACHE"], p.Dir); env["GOMODCACHE"] != "" && err == nil && !strings.HasPrefix(rel, "..") {
				entry.Module = moduleVersion(p.Module)
			}
			plain := map[string]bool{}
			for _, g := range abs(p.GoFiles) {
				plain[g] = true
			}
			cgoDir := filepath.Join("cgo", hex.EncodeToString(name[:8]))
			if err := os.MkdirAll(filepath.Join(out, cgoDir), 0o755); err != nil {
				return err
			}
			for _, c := range abs(p.CompiledGoFiles) {
				if plain[c] {
					continue
				}
				o := filepath.Join(cgoDir, filepath.Base(c))
				if err := copyFile(c, filepath.Join(out, o)); err != nil {
					return err
				}
				entry.CgoOutput = append(entry.CgoOutput, filepath.ToSlash(o))
			}
		}
		m.Packages[p.ImportPath] = entry
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "manifest.json"), append(data, '\n'), 0o644)
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
