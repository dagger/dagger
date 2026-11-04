package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Environment variables the driver reads.
const (
	// envArchives is the directory holding manifest.json and the archives.
	// Unset or empty, the driver declines every request.
	envArchives = "GOLANGCI_EXPORT_ARCHIVES"
	// envLog, if set, is a file the driver appends one JSON line per
	// request to, describing what it decided.
	envLog = "GOLANGCI_EXPORT_DRIVER_LOG"
	// envWorkDir is where compiled archives are written; they must outlive
	// the driver, since the caller reads them after it exits.
	envWorkDir = "GOLANGCI_EXPORT_WORKDIR"
	// envCompile, "never", serves the archives there are and compiles no
	// package: for a caller that type-checks the rest from source, such as
	// the Go SDK's codegen, which reads declarations only.
	envCompile = "GOLANGCI_EXPORT_COMPILE"
)

// report is the JSON line written to envLog for each request.
type report struct {
	Declined string   `json:"declined,omitempty"`
	Roots    int      `json:"roots,omitempty"`
	Prebuilt int      `json:"prebuilt,omitempty"`
	Compiled int      `json:"compiled,omitempty"`
	GoList   int      `json:"golist,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	// TestsCompiled counts the root test packages compiled to check for
	// errors only the compiler reports.
	TestsCompiled int `json:"testsCompiled,omitempty"`
	// CompileErrors are packages this driver failed to compile, which
	// fell back to the go command.
	CompileErrors []string `json:"compileErrors,omitempty"`
	// ListedCompiled is set when the manifest didn't give every package's
	// compiled files, so the go command listed them, running cgo.
	ListedCompiled bool `json:"listedCompiled,omitempty"`
}

func runDriver(ctx context.Context, patterns []string, stdin io.Reader, stdout io.Writer) error {
	var req packages.DriverRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode driver request: %w", err)
	}
	resp, rep, err := serve(ctx, &req, patterns)
	if err != nil {
		return err
	}
	if err := writeReport(rep); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(resp)
}

func writeReport(rep report) error {
	path := os.Getenv(envLog)
	if path == "" {
		return nil
	}
	line, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func declined(reason string) (*packages.DriverResponse, report, error) {
	return &packages.DriverResponse{NotHandled: true}, report{Declined: reason}, nil
}

// serve answers a request from go/packages. It declines (NotHandled, so the
// caller falls back to its own go list driver) whenever the prebuilt
// archives may not match what the go command would build, or the caller
// would type-check differently than without the driver.
func serve(ctx context.Context, req *packages.DriverRequest, patterns []string) (*packages.DriverResponse, report, error) {
	dir := os.Getenv(envArchives)
	if dir == "" {
		return declined("no archives configured")
	}
	if len(req.Overlay) > 0 {
		return declined("request has overlays")
	}
	if reason := unsupportedBuildFlags(req.BuildFlags); reason != "" {
		return declined(reason)
	}
	m, err := readManifest(dir)
	if err != nil {
		return declined("manifest: " + err.Error())
	}
	env, err := goEnv(ctx, req.Env)
	if err != nil {
		return nil, report{}, err
	}
	goflags, err := quotedSplit(env["GOFLAGS"])
	if err != nil {
		return declined("GOFLAGS: " + err.Error())
	}
	if reason := unsupportedGOFLAGS(goflags); reason != "" {
		return declined(reason)
	}
	switch {
	case m.GoVersion != env["GOVERSION"]:
		return declined(fmt.Sprintf("archives built by %s, go command is %s", m.GoVersion, env["GOVERSION"]))
	case m.GOOS != env["GOOS"] || m.GOARCH != env["GOARCH"]:
		return declined(fmt.Sprintf("archives built for %s/%s, not %s/%s", m.GOOS, m.GOARCH, env["GOOS"], env["GOARCH"]))
	case m.CgoEnabled != (env["CGO_ENABLED"] == "1"):
		return declined("archives built with a different CGO_ENABLED")
	}
	// Flags on the command line override GOFLAGS', so they come last.
	tags := buildTags(append(goflags, req.BuildFlags...))
	if !sameStrings(tags, nonNil(m.BuildTags)) {
		return declined(fmt.Sprintf("archives built with tags %v, request has %v", m.BuildTags, tags))
	}
	linterGo, err := linterGoVersion()
	if err != nil {
		return declined("linter Go version: " + err.Error())
	}

	// The packages' compiled files come from the manifest, which saves the
	// go command running cgo for every cgo package; unless it misses one.
	// go/packages lists compiled files for any of the modes that type-check,
	// which the caller does itself, from what this driver answers.
	mode := req.Mode&^(packages.NeedExportFile|packages.NeedCompiledGoFiles|
		packages.NeedSyntax|packages.NeedTypes|packages.NeedTypesInfo|packages.NeedTypesSizes) |
		packages.NeedName | packages.NeedFiles |
		packages.NeedImports | packages.NeedDeps | packages.NeedModule | packages.NeedEmbedFiles | packages.NeedEmbedPatterns
	cfg := &packages.Config{
		Context:    ctx,
		Mode:       mode,
		Env:        append(slices.Clone(req.Env), "GOPACKAGESDRIVER=off"),
		BuildFlags: req.BuildFlags,
		Tests:      req.Tests,
	}
	roots, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, report{}, err
	}
	// cgo's output in the archives is good only for the C settings it was
	// made with; with others, the go command lists cgo's output afresh.
	cgoMatches := m.CgoEnabled && maps.Equal(m.Cgo, cgoSettings(ctx, env))
	// cgo's output imports packages, such as runtime/cgo, that the go
	// command leaves out of the listing unless it lists compiled files.
	var cgoImports []*packages.Package
	if cgoMatches {
		plain := *cfg
		plain.Tests = false
		if cgoImports, err = packages.Load(&plain, "runtime/cgo", "syscall"); err != nil {
			return nil, report{}, err
		}
	}
	listedCompiled := false
	if !compiledFromManifest(m, dir, roots, cgoImports, cgoMatches, env["GOMODCACHE"]) || !cgoOutputMatches(m, dir, roots) {
		cfg.Mode |= packages.NeedCompiledGoFiles
		if roots, err = packages.Load(cfg, patterns...); err != nil {
			return nil, report{}, err
		}
		listedCompiled = true
	}

	// Through a driver the caller loses Module, so it type-checks with the
	// Go version it was built with instead of the module's. Only serve
	// modules whose language version that is.
	for _, root := range roots {
		if root.Module == nil || root.Module.GoVersion == "" {
			return declined("package " + root.ID + " has no module Go version")
		}
		if langVersion(root.Module.GoVersion) != langVersion(linterGo) {
			return declined(fmt.Sprintf("module %s is go %s, linter built with %s", root.Module.Path, root.Module.GoVersion, linterGo))
		}
	}

	rep := report{Roots: len(roots), ListedCompiled: listedCompiled}
	if req.Mode&packages.NeedExportFile != 0 {
		if err := assignExportData(ctx, cfg, m, dir, roots, &rep); err != nil {
			return nil, report{}, err
		}
	}

	var all []*packages.Package
	packages.Visit(roots, nil, func(p *packages.Package) { all = append(all, p) })
	resp := &packages.DriverResponse{
		Compiler:  "gc",
		Arch:      env["GOARCH"],
		Packages:  all,
		GoVersion: goMinor(env["GOVERSION"]),
	}
	for _, r := range roots {
		resp.Roots = append(resp.Roots, r.ID)
	}
	return resp, rep, nil
}

// compiledFromManifest sets each package's compiled Go files as the go
// command would list them, and reports whether it could for all of them. A
// package in the manifest compiles its Go files, or for a cgo package, when
// cgoMatches and its content is fixed (fixedContent), its other Go files and
// cgo's output in the manifest's directory. A package with no Go files,
// unsafe and a test main, whose _testmain.go has no cgo, compile their Go
// files too, as does any other package none of whose Go files imports "C":
// that is how the go command tells a cgo package. Only a cgo package the
// manifest lacks needs the go command to list what cgo writes for it.
//
// cgo's output imports packages the cgo files don't, such as runtime/cgo
// and syscall, which the go command lists among a cgo package's imports only
// when it lists the compiled files: they are added from the packages loaded,
// roots' or extra's, one package for each ID.
func compiledFromManifest(m *manifest, dir string, roots, extra []*packages.Package, cgoMatches bool, modcache string) bool {
	byID := map[string]*packages.Package{}
	packages.Visit(roots, nil, func(p *packages.Package) { byID[p.ID] = p })
	packages.Visit(extra, nil, func(p *packages.Package) {
		for path, imp := range p.Imports {
			p.Imports[path] = byID[imp.ID]
		}
		if _, ok := byID[p.ID]; !ok {
			byID[p.ID] = p
		}
	})
	byPath := map[string]*packages.Package{}
	for id, p := range byID {
		if id == p.PkgPath {
			byPath[id] = p
		}
	}
	// Every package reached from the roots, through the imports added too.
	ok := true
	done := map[*packages.Package]bool{}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if done[p] {
			continue
		}
		done[p] = true
		e, listed := m.Packages[p.ID]
		switch {
		case listed && len(e.CgoOutput) > 0 && (!cgoMatches || !fixedContent(p, e, modcache)):
			ok = false
		case listed && len(e.CgoOutput) > 0:
			cgo := map[string]bool{}
			for _, f := range e.CgoFiles {
				cgo[filepath.Join(p.Dir, f)] = true
			}
			p.CompiledGoFiles = nil
			for _, g := range p.GoFiles {
				if !cgo[g] {
					p.CompiledGoFiles = append(p.CompiledGoFiles, g)
				}
			}
			for _, o := range e.CgoOutput {
				out := filepath.Join(dir, filepath.FromSlash(o))
				p.CompiledGoFiles = append(p.CompiledGoFiles, out)
				if !addImports(p, out, byPath) {
					ok = false
				}
			}
		case listed, len(p.GoFiles) == 0, p.PkgPath == "unsafe", isTestMain(p), !importsC(p.GoFiles) && !hasSwig(p.OtherFiles):
			p.CompiledGoFiles = slices.Clone(p.GoFiles)
		default:
			ok = false
		}
		for _, imp := range p.Imports {
			queue = append(queue, imp)
		}
	}
	return ok
}

// cgoOutputMatches reports whether every package given the manifest's copy of
// cgo's output was compiled from its sources as they are. One that wasn't
// gets fresh export data, which must be checked against cgo's fresh output.
func cgoOutputMatches(m *manifest, dir string, roots []*packages.Package) bool {
	ok := true
	packages.Visit(roots, nil, func(p *packages.Package) {
		if e := m.Packages[p.ID]; len(e.CgoOutput) > 0 && !verifyPrebuilt(m, dir, p) {
			ok = false
		}
	})
	return ok
}

// importsC reports whether any of files imports "C", or can't be read to
// tell.
func importsC(files []string) bool {
	for _, f := range files {
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			return true
		}
		for _, spec := range file.Imports {
			if spec.Path.Value == `"C"` {
				return true
			}
		}
	}
	return false
}

// hasSwig reports whether a package has SWIG files, which make it use cgo
// too.
func hasSwig(files []string) bool {
	for _, f := range files {
		if strings.HasSuffix(f, ".swig") || strings.HasSuffix(f, ".swigcxx") {
			return true
		}
	}
	return false
}

// fixedContent reports whether what a cgo package's C compiles read, which
// can be any file of its module, is fixed by what identifies it and is the
// same as the archives': the standard library's, by the Go version, or a
// module version's in the module cache, by its checksum, if the archives
// were made from that version. A package of the main module, of a local
// replacement or of a vendor directory can include files its digest
// doesn't cover, so the go command runs cgo for it afresh.
func fixedContent(p *packages.Package, e manifestEntry, modcache string) bool {
	if p.Module == nil {
		return true
	}
	v := moduleVersion(p.Module)
	rel, err := filepath.Rel(modcache, p.Dir)
	return !p.Module.Main && v != "" && v == e.Module && modcache != "" && err == nil && !strings.HasPrefix(rel, "..")
}

// addImports adds to p's imports the packages file imports that p's don't
// have, from byPath, and reports whether it found them all. C is cgo's, no
// package.
func addImports(p *packages.Package, file string, byPath map[string]*packages.Package) bool {
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return false
		}
		if _, ok := p.Imports[path]; ok || path == "C" {
			continue
		}
		imp, ok := byPath[path]
		if !ok {
			return false
		}
		if p.Imports == nil {
			p.Imports = map[string]*packages.Package{}
		}
		p.Imports[path] = imp
	}
	return true
}

// unsupportedBuildFlags rejects build flags that change what gets compiled
// in ways the manifest doesn't record. golangci-lint passes only -tags, -mod
// and -buildvcs, which stamps binaries only.
func unsupportedBuildFlags(flags []string) string {
	for i := 0; i < len(flags); i++ {
		f := strings.TrimPrefix(flags[i], "-")
		f = strings.TrimPrefix(f, "-")
		switch {
		case f == "tags":
			i++
		case strings.HasPrefix(f, "tags="), strings.HasPrefix(f, "mod="), f == "modcacherw",
			f == "buildvcs", strings.HasPrefix(f, "buildvcs="):
		default:
			return "unsupported build flag " + flags[i]
		}
	}
	return ""
}

func unsupportedGOFLAGS(goflags []string) string {
	if reason := unsupportedBuildFlags(goflags); reason != "" {
		return "GOFLAGS: " + reason
	}
	return ""
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func goEnv(ctx context.Context, env []string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"env", "-json", "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOMODCACHE"}, cgoVars...)...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w: %s", err, stderr.String())
	}
	vals := map[string]string{}
	if err := json.Unmarshal(out, &vals); err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	return vals, nil
}

// linterGoVersion is the Go version the calling linter (the driver's parent
// process) was built with: the version it type-checks with when a package
// has no Module, as packages served by a driver don't. Tests replace it.
var linterGoVersion = parentGoVersion

func parentGoVersion() (string, error) {
	info, err := buildinfo.ReadFile(fmt.Sprintf("/proc/%d/exe", os.Getppid()))
	if err != nil {
		return "", err
	}
	return info.GoVersion, nil
}

// langVersion reduces a Go version ("go1.26.3", "1.26.6", "1.22") to its
// language version ("1.26").
func langVersion(v string) string {
	v = strings.TrimPrefix(v, "go")
	if i := strings.IndexAny(v, " -+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	minor := parts[1]
	for i, r := range minor {
		if r < '0' || r > '9' {
			minor = minor[:i]
			break
		}
	}
	return parts[0] + "." + minor
}

func goMinor(v string) int {
	parts := strings.Split(langVersion(v), ".")
	if len(parts) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(parts[1])
	return n
}

func workDir() (string, error) {
	if d := os.Getenv(envWorkDir); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
		return os.MkdirTemp(d, "export-")
	}
	return os.MkdirTemp("", "golangci-export-")
}

func absArchive(dir, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(dir, rel)
}
