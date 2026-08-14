//! Engine-free native-platform observation and three-OS matrix assembly.
//!
//! The producer owns one fixed Cargo invocation. Callers choose only output locations; they cannot
//! inject commands, packages, filters, engines, containers, or foreign SDK work.

use std::ffi::OsString;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::{Command, ExitCode, Stdio};

use clap::{Arg, ArgAction, Command as ClapCommand, value_parser};
use dagger_sdk_completeness::{
    Architecture, CanonicalSet, ConformanceFormatVersion, Digest, NativeJobOutcome,
    NativeLinkMechanism, NativePlatformObservation, OperatingSystem, PlatformDescriptor,
    PortablePlatformMatrixInput, ReviewedConformanceScope, SemverVersion,
    assemble_development_native_platform_set, assemble_portable_platform_matrix, canonical_bytes,
    decode_canonical, release_descriptor_matrix, required_native_platform_domains,
};

const NATIVE_TEST_ARGUMENTS: &[&str] = &[
    "test",
    "-p",
    "dagger-sdk",
    "--lib",
    "--all-features",
    "--locked",
];

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            eprintln!("{message}");
            ExitCode::from(1)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = ClapCommand::new("dagger-rust-sdk-platform")
        .about("Produce and aggregate engine-free Rust SDK platform observations")
        .subcommand_required(true)
        .subcommand(
            ClapCommand::new("native")
                .about("Run the fixed native Rust SDK suite and emit one observation")
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("aggregate-development")
                .about("Admit matching Linux and macOS observations without claiming portability")
                .arg(path_argument("scope"))
                .arg(
                    Arg::new("input")
                        .long("input")
                        .required(true)
                        .action(ArgAction::Append)
                        .value_parser(value_parser!(PathBuf)),
                )
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("aggregate")
                .about("Admit three native observations and the pure descriptor matrix")
                .arg(path_argument("scope"))
                .arg(
                    Arg::new("input")
                        .long("input")
                        .required(true)
                        .action(ArgAction::Append)
                        .value_parser(value_parser!(PathBuf)),
                )
                .arg(path_argument("output")),
        )
        .get_matches();
    match matches.subcommand().expect("subcommand is required") {
        ("native", values) => native(values.get_one::<PathBuf>("output").unwrap()),
        ("aggregate-development", values) => aggregate_development(
            values.get_one::<PathBuf>("scope").unwrap(),
            values
                .get_many::<PathBuf>("input")
                .expect("input is required")
                .cloned()
                .collect(),
            values.get_one::<PathBuf>("output").unwrap(),
        ),
        ("aggregate", values) => aggregate(
            values.get_one::<PathBuf>("scope").unwrap(),
            values
                .get_many::<PathBuf>("input")
                .expect("input is required")
                .cloned()
                .collect(),
            values.get_one::<PathBuf>("output").unwrap(),
        ),
        _ => unreachable!("the command vocabulary is closed"),
    }
}

fn aggregate_development(
    scope: &Path,
    inputs: Vec<PathBuf>,
    output: &Path,
) -> Result<(), &'static str> {
    if inputs.len() != 2 {
        return Err("development platform aggregation requires exactly two observations");
    }
    let scope: ReviewedConformanceScope =
        decode_canonical(&fs::read(scope).map_err(|_| "could not read checked conformance scope")?)
            .map_err(|_| "checked conformance scope is not canonical")?;
    let observations = read_observations(inputs)?;
    let set = assemble_development_native_platform_set(scope.target_digest, observations)
        .map_err(|_| "development native observation admission failed")?;
    write_new(
        output,
        &canonical_bytes(&set).map_err(|_| "could not encode development observation set")?,
    )
}

fn path_argument(name: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .required(true)
        .value_parser(value_parser!(PathBuf))
}

fn native(output: &Path) -> Result<(), &'static str> {
    let rust_root = rust_root();
    let rustc = process_output("rustc", ["--version", "--verbose"], &rust_root)?;
    let rust_version = parse_rust_version(&rustc)?;
    if rust_version != SemverVersion::new("1.97.1").expect("checked version is valid") {
        return Err("native platform job did not use Rust 1.97.1");
    }

    let status = Command::new("cargo")
        .args(NATIVE_TEST_ARGUMENTS)
        .current_dir(&rust_root)
        .stdin(Stdio::null())
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit())
        .status()
        .map_err(|_| "could not start the fixed native Rust SDK suite")?;
    if !status.success() {
        return Err("fixed native Rust SDK suite failed");
    }

    let platform = native_platform()?;
    let runner_digest = runner_digest(&rustc);
    let toolchain_digest = Digest::sha256(
        [
            rustc.as_slice(),
            &fs::read(rust_root.join("rust-toolchain.toml"))
                .map_err(|_| "could not read the pinned Rust toolchain")?,
        ]
        .concat(),
    );
    let observation = NativePlatformObservation {
        format_version: ConformanceFormatVersion::V1,
        link_mechanism: match platform.operating_system {
            OperatingSystem::Linux | OperatingSystem::Macos => NativeLinkMechanism::PosixSymlink,
            OperatingSystem::Windows => NativeLinkMechanism::WindowsReparseOrAcl,
        },
        platform,
        runner_digest,
        toolchain_digest,
        rust_version,
        source_digest: source_digest(&rust_root)?,
        lockfiles_digest: lockfiles_digest(&rust_root)?,
        test_digest: test_digest(),
        domains: CanonicalSet::new(required_native_platform_domains()),
        outcome: NativeJobOutcome::Passed,
        native_execution: true,
        dagger_invocations: 0,
        engine_starts: 0,
        docker_invocations: 0,
        other_sdk_invocations: 0,
    };
    write_new(
        output,
        &canonical_bytes(&observation).map_err(|_| "could not encode observation")?,
    )
}

fn aggregate(scope: &Path, inputs: Vec<PathBuf>, output: &Path) -> Result<(), &'static str> {
    if inputs.len() != 3 {
        return Err("platform aggregation requires exactly three observations");
    }
    let scope: ReviewedConformanceScope =
        decode_canonical(&fs::read(scope).map_err(|_| "could not read checked conformance scope")?)
            .map_err(|_| "checked conformance scope is not canonical")?;
    let native_observations = read_observations(inputs)?;
    let input = PortablePlatformMatrixInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest: scope.target_digest,
        native_observations,
        descriptors: release_descriptor_matrix(
            &SemverVersion::new("1.0.0-beta.10").expect("checked CLI version is valid"),
        )
        .into_inner(),
    };
    let matrix = assemble_portable_platform_matrix(input)
        .map_err(|_| "portable platform matrix admission failed")?;
    write_new(
        output,
        &canonical_bytes(&matrix).map_err(|_| "could not encode platform matrix")?,
    )
}

fn read_observations(inputs: Vec<PathBuf>) -> Result<Vec<NativePlatformObservation>, &'static str> {
    inputs
        .into_iter()
        .map(|path| {
            decode_canonical(&fs::read(path).map_err(|_| "could not read native observation")?)
                .map_err(|_| "native observation is not canonical")
        })
        .collect()
}

fn rust_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../..")
        .components()
        .collect()
}

fn process_output<I, S>(program: &str, args: I, cwd: &Path) -> Result<Vec<u8>, &'static str>
where
    I: IntoIterator<Item = S>,
    S: Into<OsString>,
{
    let output = Command::new(program)
        .args(args.into_iter().map(Into::into))
        .current_dir(cwd)
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output()
        .map_err(|_| "could not execute native identity probe")?;
    if !output.status.success() || output.stdout.len() > 64 * 1024 {
        return Err("native identity probe failed or exceeded its bound");
    }
    Ok(output.stdout)
}

fn parse_rust_version(output: &[u8]) -> Result<SemverVersion, &'static str> {
    let text = std::str::from_utf8(output).map_err(|_| "rustc identity is not UTF-8")?;
    let version = text
        .lines()
        .next()
        .and_then(|line| line.strip_prefix("rustc "))
        .and_then(|line| line.split_whitespace().next())
        .ok_or("rustc identity has an unknown shape")?;
    SemverVersion::new(version).map_err(|_| "rustc version is not semantic")
}

fn native_platform() -> Result<PlatformDescriptor, &'static str> {
    let operating_system = match std::env::consts::OS {
        "linux" => OperatingSystem::Linux,
        "macos" => OperatingSystem::Macos,
        "windows" => OperatingSystem::Windows,
        _ => return Err("native operating system is outside the supported matrix"),
    };
    let architecture = match std::env::consts::ARCH {
        "x86_64" => Architecture::Amd64,
        "aarch64" => Architecture::Arm64,
        _ => return Err("native architecture is outside the supported matrix"),
    };
    Ok(PlatformDescriptor {
        operating_system,
        architecture,
    })
}

fn runner_digest(rustc: &[u8]) -> Digest {
    let mut identity = Vec::new();
    identity.extend_from_slice(std::env::consts::OS.as_bytes());
    identity.push(0);
    identity.extend_from_slice(std::env::consts::ARCH.as_bytes());
    identity.push(0);
    identity.extend_from_slice(rustc);
    for name in ["ImageOS", "ImageVersion", "RUNNER_ARCH"] {
        identity.push(0);
        if let Some(value) = std::env::var_os(name) {
            identity.extend_from_slice(value.to_string_lossy().as_bytes());
        }
    }
    Digest::sha256(identity)
}

fn source_digest(rust_root: &Path) -> Result<Digest, &'static str> {
    let mut paths = Vec::new();
    collect_files(&rust_root.join("crates/dagger-sdk/src"), &mut paths)?;
    for relative in [
        "Cargo.toml",
        "crates/dagger-sdk/Cargo.toml",
        "rust-toolchain.toml",
    ] {
        paths.push(rust_root.join(relative));
    }
    digest_files(rust_root, paths)
}

fn lockfiles_digest(rust_root: &Path) -> Result<Digest, &'static str> {
    digest_files(
        rust_root,
        [
            "Cargo.lock",
            "crates/dagger-codegen/Cargo.lock",
            "examples/backend/Cargo.lock",
            "examples/cli/Cargo.lock",
            "examples/frontend/Cargo.lock",
        ]
        .into_iter()
        .map(|relative| rust_root.join(relative))
        .collect(),
    )
}

fn test_digest() -> Digest {
    let mut bytes = NATIVE_TEST_ARGUMENTS.join("\0").into_bytes();
    for domain in required_native_platform_domains() {
        bytes.extend_from_slice(format!("\0{domain:?}").as_bytes());
    }
    Digest::sha256(bytes)
}

fn collect_files(directory: &Path, paths: &mut Vec<PathBuf>) -> Result<(), &'static str> {
    for entry in fs::read_dir(directory).map_err(|_| "could not enumerate native suite source")? {
        let entry = entry.map_err(|_| "could not enumerate native suite source")?;
        let file_type = entry
            .file_type()
            .map_err(|_| "could not inspect native suite source")?;
        if file_type.is_dir() {
            collect_files(&entry.path(), paths)?;
        } else if file_type.is_file() {
            paths.push(entry.path());
        } else {
            return Err("native suite source contains a non-file entry");
        }
    }
    Ok(())
}

fn digest_files(root: &Path, mut paths: Vec<PathBuf>) -> Result<Digest, &'static str> {
    paths.sort_unstable();
    let repository_root: PathBuf = root
        .join("../..")
        .canonicalize()
        .map_err(|_| "could not resolve the repository root")?;
    let physical_rust_root = root
        .canonicalize()
        .map_err(|_| "could not resolve the Rust workspace root")?;
    let mut bytes = Vec::new();
    for path in paths {
        // Windows may add a verbatim prefix while canonicalizing the root. Resolve both
        // sides so containment remains fail-closed without rejecting an equivalent path.
        let physical = path
            .canonicalize()
            .map_err(|_| "could not resolve native suite source")?;
        let relative = physical
            .strip_prefix(&repository_root)
            .map_err(|_| "native suite source escaped the repository root")?
            .to_string_lossy()
            .replace('\\', "/");
        let rust_relative = physical
            .strip_prefix(&physical_rust_root)
            .map_err(|_| "native suite source escaped the Rust workspace")?;
        let object = Command::new("git")
            .arg("-C")
            // The lexical Rust root is already accepted by Cargo on this host and avoids
            // passing Git for Windows the canonical root's verbatim path representation.
            .arg(root)
            .arg("hash-object")
            // Applying the path's Git attributes makes the identity independent of checkout
            // line endings while still hashing dirty working-tree bytes.
            .arg(format!("--path={relative}"))
            // Canonical paths prove containment; Git opens the same file relative to the
            // Rust root so neither its working directory nor input uses a verbatim path.
            .arg(rust_relative)
            .stdin(Stdio::null())
            .stderr(Stdio::null())
            .output()
            .map_err(|_| "could not hash native suite source")?;
        if !object.status.success() {
            return Err("could not hash native suite source");
        }
        bytes.extend_from_slice(relative.as_bytes());
        bytes.push(0);
        bytes.extend_from_slice(&object.stdout);
        bytes.push(0);
    }
    Ok(Digest::sha256(bytes))
}

fn write_new(path: &Path, bytes: &[u8]) -> Result<(), &'static str> {
    let mut options = fs::OpenOptions::new();
    options.create_new(true).write(true);
    use std::io::Write as _;
    let mut file = options
        .open(path)
        .map_err(|_| "could not create platform output")?;
    file.write_all(bytes)
        .and_then(|()| file.sync_all())
        .map_err(|_| "could not persist platform output")
}
