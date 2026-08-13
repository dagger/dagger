//! Private typed host-preflight and SDK-sign-off command boundary.
//!
//! The initial command accepts one checked profile and one output path. All executed host actions
//! are selected by the closed Rust plan; callers cannot supply provider arguments or commands.

use std::collections::BTreeMap;
use std::ffi::OsStr;
use std::fs;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Command, ExitCode, Stdio};
use std::thread;
use std::time::{Duration, Instant};

use clap::{Arg, Command as ClapCommand, value_parser};
use dagger_sdk_completeness::{
    ArtifactCounters, ArtifactEvent, ArtifactMaterialization, ArtifactObservation, ArtifactPlan,
    CanonicalSet, ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ContainerDaemonObservation, DiagnosticCoordinate, DiagnosticPhase, Digest, HostPreflightPlan,
    HostPreflightRecord, HostPreflightStep, HostProbe, HostProbeError, HostProbeErrorKind,
    HostResourceObservation, HostStepObservation, HostStepResult, NonEmptyText, NonZeroBytes,
    NonZeroCount, NonZeroMillis, PlatformDescriptor, SignoffHostProfile, admit_artifact,
    artifact_manifest_for_payload, artifact_provenance_document, assemble_artifact_bundle,
    canonical_bytes, decode_artifact_bundle, decode_canonical, plan_host_preflight,
    required_artifact_components, run_host_preflight, scan_retained_output,
};

const MAX_PROCESS_OUTPUT: usize = 1024 * 1024;
const CANARY: &[u8] = b"dagger-preflight-canary-80986dbe61454709a738130c13f3d0db";
const SMOKE_CONTAINER: &str = "dagger-rust-sdk-preflight-smoke";
const CACHE_IMAGE: &str = "dagger-rust-sdk-preflight-cache:current";
const SMOKE_IMAGE: &str = "registry.dagger.io/engine@sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e";

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(BinaryError::Contract(errors)) => {
            for error in errors.as_slice() {
                eprintln!("{} {:?}", error.code, error.coordinate.phase);
            }
            ExitCode::from(1)
        }
        Err(BinaryError::Operational(message)) => {
            eprintln!("{message}");
            ExitCode::from(2)
        }
    }
}

enum BinaryError {
    Contract(dagger_sdk_completeness::ConformanceDiagnosticSet),
    Operational(&'static str),
}

impl From<dagger_sdk_completeness::ConformanceDiagnosticSet> for BinaryError {
    fn from(value: dagger_sdk_completeness::ConformanceDiagnosticSet) -> Self {
        Self::Contract(value)
    }
}

fn run() -> Result<(), BinaryError> {
    let matches = ClapCommand::new("dagger-rust-sdk-signoff")
        .about("Private Dagger Rust SDK sign-off tooling")
        .subcommand_required(true)
        .subcommand(
            ClapCommand::new("preflight")
                .about("Validate one checked provider-neutral sign-off host profile")
                .arg(
                    Arg::new("profile")
                        .long("profile")
                        .required(true)
                        .value_parser(value_parser!(PathBuf)),
                )
                .arg(
                    Arg::new("output")
                        .long("output")
                        .required(true)
                        .value_parser(value_parser!(PathBuf)),
                ),
        )
        .subcommand(
            ClapCommand::new("artifact-build")
                .about("Assemble one canonical exact-target bundle from existing OCI bytes")
                .arg(path_argument("plan"))
                .arg(path_argument("payload"))
                .arg(path_argument("bundle-output"))
                .arg(path_argument("manifest-output")),
        )
        .subcommand(
            ClapCommand::new("artifact-import")
                .about("Verify one exact-target bundle and extract its admitted OCI bytes")
                .arg(path_argument("plan"))
                .arg(path_argument("bundle"))
                .arg(path_argument("payload-output")),
        )
        .get_matches();
    match matches.subcommand().expect("subcommand is required") {
        ("preflight", values) => preflight(
            required_path(values, "profile"),
            required_path(values, "output"),
        ),
        ("artifact-build", values) => artifact_build(
            required_path(values, "plan"),
            required_path(values, "payload"),
            required_path(values, "bundle-output"),
            required_path(values, "manifest-output"),
        ),
        ("artifact-import", values) => artifact_import(
            required_path(values, "plan"),
            required_path(values, "bundle"),
            required_path(values, "payload-output"),
        ),
        _ => unreachable!("clap admits only the closed sign-off commands"),
    }
}

fn path_argument(name: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .required(true)
        .value_parser(value_parser!(PathBuf))
}

fn required_path<'a>(values: &'a clap::ArgMatches, name: &str) -> &'a PathBuf {
    values
        .get_one::<PathBuf>(name)
        .expect("clap requires every path argument")
}

fn artifact_build(
    plan_path: &Path,
    payload_path: &Path,
    bundle_output: &Path,
    manifest_output: &Path,
) -> Result<(), BinaryError> {
    let plan = read_artifact_plan(plan_path)?;
    if !matches!(plan.materialization, ArtifactMaterialization::Build) {
        return Err(BinaryError::Operational(
            "artifact-build requires the Build strategy",
        ));
    }
    let payload = fs::read(payload_path)
        .map_err(|_| BinaryError::Operational("could not read exact-target OCI payload"))?;
    let manifest = artifact_manifest_for_payload(&plan, &payload)?;
    let provenance = artifact_provenance_document(&plan)?;
    let bundle = assemble_artifact_bundle(manifest.clone(), provenance, payload)?;
    let component_builds = required_artifact_components()
        .into_iter()
        .map(|component| (component, 1))
        .collect::<BTreeMap<_, _>>();
    let mut events = vec![ArtifactEvent::ConstructionStarted];
    events.extend(
        component_builds
            .keys()
            .copied()
            .map(|component| ArtifactEvent::ComponentBuilt { component }),
    );
    events.extend([
        ArtifactEvent::PayloadExported,
        ArtifactEvent::ManifestVerified,
        ArtifactEvent::PayloadVerified,
        ArtifactEvent::ComponentsVerified,
        ArtifactEvent::ArtifactReady,
    ]);
    let admitted = admit_artifact(
        &plan,
        ArtifactObservation {
            strategy: ArtifactMaterialization::Build,
            manifest: manifest.clone(),
            verified_component_digests: component_digests(&manifest),
            bundle,
            events,
            counters: ArtifactCounters {
                construction: 1,
                imports: 0,
                component_builds,
                forbidden_work: CanonicalSet::default(),
            },
            elapsed_millis: 1,
        },
    )?;
    write_new(bundle_output, admitted.bundle().bytes())?;
    let manifest_bytes = canonical_bytes(&manifest)
        .map_err(|_| BinaryError::Operational("could not encode exact-target manifest"))?;
    write_new(manifest_output, &manifest_bytes)
}

fn artifact_import(
    plan_path: &Path,
    bundle_path: &Path,
    payload_output: &Path,
) -> Result<(), BinaryError> {
    let plan = read_artifact_plan(plan_path)?;
    if !matches!(plan.materialization, ArtifactMaterialization::Import { .. }) {
        return Err(BinaryError::Operational(
            "artifact-import requires the Import strategy",
        ));
    }
    let bytes = fs::read(bundle_path)
        .map_err(|_| BinaryError::Operational("could not read exact-target artifact bundle"))?;
    let bundle = decode_artifact_bundle(&bytes)?;
    let manifest = bundle.manifest().clone();
    let admitted = admit_artifact(
        &plan,
        ArtifactObservation {
            strategy: plan.materialization.clone(),
            manifest: manifest.clone(),
            verified_component_digests: component_digests(&manifest),
            bundle,
            events: vec![
                ArtifactEvent::BundleSupplied,
                ArtifactEvent::ManifestVerified,
                ArtifactEvent::PayloadVerified,
                ArtifactEvent::ComponentsVerified,
                ArtifactEvent::ContainerImported,
                ArtifactEvent::ArtifactReady,
            ],
            counters: ArtifactCounters {
                construction: 0,
                imports: 1,
                component_builds: required_artifact_components()
                    .into_iter()
                    .map(|component| (component, 0))
                    .collect(),
                forbidden_work: CanonicalSet::default(),
            },
            elapsed_millis: 1,
        },
    )?;
    write_new(payload_output, admitted.bundle().payload())
}

fn read_artifact_plan(path: &Path) -> Result<ArtifactPlan, BinaryError> {
    let bytes = fs::read(path)
        .map_err(|_| BinaryError::Operational("could not read exact-target artifact plan"))?;
    decode_canonical(&bytes)
        .map_err(|_| BinaryError::Operational("exact-target artifact plan is not canonical"))
}

fn component_digests(
    manifest: &dagger_sdk_completeness::ExactTargetArtifactManifest,
) -> BTreeMap<dagger_sdk_completeness::ArtifactComponent, Digest> {
    manifest
        .components
        .iter()
        .map(|(component, record)| (*component, record.content_digest.clone()))
        .collect()
}

fn preflight(profile_path: &Path, output_path: &Path) -> Result<(), BinaryError> {
    let profile_bytes = fs::read(profile_path)
        .map_err(|_| BinaryError::Operational("could not read checked host profile"))?;
    let profile: SignoffHostProfile = decode_canonical(&profile_bytes)
        .map_err(|_| BinaryError::Operational("checked host profile is not canonical"))?;
    let plan = plan_host_preflight(profile)?;
    let mut probe = ProcessHostProbe::new(&plan)?;
    let result = run_host_preflight(&plan, &mut probe);
    let cleanup = probe.cleanup();
    let record = match (result, cleanup) {
        (Ok(record), Ok(())) => record,
        (Err(errors), Ok(())) => return Err(BinaryError::Contract(errors)),
        (Err(errors), Err(_)) => {
            let mut diagnostics = errors.as_slice().to_vec();
            diagnostics.push(cleanup_diagnostic());
            return Err(BinaryError::Contract(
                ConformanceDiagnosticSet::new(diagnostics)
                    .expect("primary and cleanup diagnostics are non-empty"),
            ));
        }
        (Ok(_), Err(_)) => {
            return Err(BinaryError::Contract(
                ConformanceDiagnosticSet::new([cleanup_diagnostic()])
                    .expect("cleanup diagnostic is non-empty"),
            ));
        }
    };
    write_record(output_path, &record)
}

fn write_record(path: &Path, record: &HostPreflightRecord) -> Result<(), BinaryError> {
    let bytes = canonical_bytes(record)
        .map_err(|_| BinaryError::Operational("could not encode preflight record"))?;
    write_new(path, &bytes)
}

fn write_new(path: &Path, bytes: &[u8]) -> Result<(), BinaryError> {
    if path.exists() {
        return Err(BinaryError::Operational("sign-off output already exists"));
    }
    let parent = path
        .parent()
        .ok_or(BinaryError::Operational("sign-off output has no parent"))?;
    fs::create_dir_all(parent)
        .map_err(|_| BinaryError::Operational("could not create preflight output directory"))?;
    let temporary = parent.join(format!(
        ".dagger-rust-sdk-preflight-{}.tmp",
        std::process::id()
    ));
    let mut file = fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(&temporary)
        .map_err(|_| BinaryError::Operational("could not create sign-off output"))?;
    file.write_all(bytes)
        .and_then(|()| file.sync_all())
        .map_err(|_| BinaryError::Operational("could not persist sign-off output"))?;
    fs::rename(&temporary, path)
        .map_err(|_| BinaryError::Operational("could not publish sign-off output"))?;
    Ok(())
}

struct ProcessHostProbe {
    budgets: BTreeMap<dagger_sdk_completeness::HostPreflightPhase, NonZeroMillis>,
    workspace: PathBuf,
    retained: Vec<Vec<u8>>,
    smoke_started: bool,
    cache_image_created: bool,
    cleaned: bool,
    smoke_tool: dagger_sdk_completeness::ProvenanceId,
    smoke_engine: dagger_sdk_completeness::ProvenanceId,
}

impl ProcessHostProbe {
    fn new(plan: &HostPreflightPlan) -> Result<Self, BinaryError> {
        let workspace = std::env::current_dir()
            .map_err(|_| BinaryError::Operational("could not resolve preflight workspace"))?
            .join(format!(".dagger-rust-sdk-preflight-{}", std::process::id()));
        fs::create_dir(&workspace)
            .map_err(|_| BinaryError::Operational("could not create preflight workspace"))?;
        let current_exe = std::env::current_exe()
            .map_err(|_| BinaryError::Operational("could not resolve preflight binary"))?;
        let binary_digest = Digest::sha256(
            fs::read(current_exe)
                .map_err(|_| BinaryError::Operational("could not read preflight binary"))?,
        );
        let expected_binary_digest = plan
            .profile
            .preflight_tool
            .as_str()
            .rsplit('/')
            .next()
            .and_then(|hex| Digest::new(format!("sha256:{hex}")).ok())
            .ok_or(BinaryError::Operational(
                "preflight binary provenance is invalid",
            ))?;
        if binary_digest != expected_binary_digest {
            return Err(BinaryError::Operational(
                "preflight binary provenance does not match profile",
            ));
        }
        Ok(Self {
            budgets: plan.profile.phase_budgets.clone(),
            workspace,
            retained: Vec::new(),
            smoke_started: false,
            cache_image_created: false,
            cleaned: false,
            smoke_tool: plan.profile.smoke_tool.clone(),
            smoke_engine: plan.profile.smoke_engine.clone(),
        })
    }

    fn budget(&self, step: HostPreflightStep) -> Duration {
        Duration::from_millis(
            self.budgets
                .get(&step.phase())
                .expect("validated plan has every phase budget")
                .get(),
        )
    }

    fn command<I, S>(
        &mut self,
        step: HostPreflightStep,
        program: &str,
        args: I,
    ) -> Result<ProcessOutput, HostProbeError>
    where
        I: IntoIterator<Item = S>,
        S: AsRef<OsStr>,
    {
        let output = run_bounded(program, args, self.budget(step))
            .map_err(|kind| HostProbeError { step, kind })?;
        self.retained.push(output.stdout.clone());
        self.retained.push(output.stderr.clone());
        if output.status != 0 {
            return Err(HostProbeError {
                step,
                kind: HostProbeErrorKind::Unavailable,
            });
        }
        Ok(output)
    }

    fn cleanup(&mut self) -> Result<(), HostProbeErrorKind> {
        if self.cleaned {
            return Ok(());
        }
        let mut failed = false;
        if self.smoke_started {
            failed |= !run_bounded(
                "docker",
                ["rm", "--force", SMOKE_CONTAINER],
                Duration::from_secs(60),
            )
            .is_ok_and(|output| output.status == 0);
            self.smoke_started = false;
        }
        if self.cache_image_created {
            failed |= !run_bounded(
                "docker",
                ["image", "rm", "--force", CACHE_IMAGE],
                Duration::from_secs(60),
            )
            .is_ok_and(|output| output.status == 0);
            self.cache_image_created = false;
        }
        failed |= fs::remove_dir_all(&self.workspace).is_err();
        self.cleaned = true;
        if failed {
            Err(HostProbeErrorKind::CleanupFailed)
        } else {
            Ok(())
        }
    }

    fn observe_host(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let os = text(step, self.command(step, "uname", ["-s"])?.stdout)?;
        let arch = text(step, self.command(step, "uname", ["-m"])?.stdout)?;
        let cpu_count = std::thread::available_parallelism()
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?
            .get();
        let memory = fs::read_to_string("/proc/meminfo")
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let memory_kib = memory
            .lines()
            .find_map(|line| {
                line.strip_prefix("MemTotal:")?
                    .split_whitespace()
                    .next()?
                    .parse::<u64>()
                    .ok()
            })
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let workspace = text(
            step,
            self.command(step, "df", ["--output=avail", "-B1", "."])?
                .stdout,
        )?;
        let workspace_bytes = workspace
            .lines()
            .last()
            .and_then(|line| line.trim().parse::<u64>().ok())
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let platform = match (os.as_str(), arch.as_str()) {
            ("Linux", "x86_64") => PlatformDescriptor::linux_amd64(),
            _ => return Err(probe_error(step, HostProbeErrorKind::InvalidOutput)),
        };
        Ok(HostStepResult::HostResources {
            observation: HostResourceObservation {
                platform,
                cpu_count: NonZeroCount::new(
                    u32::try_from(cpu_count)
                        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                )
                .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                memory_bytes: NonZeroBytes::new(memory_kib.saturating_mul(1024))
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                workspace_bytes: NonZeroBytes::new(workspace_bytes)
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
            },
        })
    }

    fn observe_daemon(
        &mut self,
        step: HostPreflightStep,
    ) -> Result<HostStepResult, HostProbeError> {
        let version = text(
            step,
            self.command(
                step,
                "docker",
                [
                    "version",
                    "--format",
                    "{{.Client.Version}}|{{.Server.Version}}|{{.Server.APIVersion}}",
                ],
            )?
            .stdout,
        )?;
        let fields = version.split('|').collect::<Vec<_>>();
        if fields.len() != 3 || fields[0] != "29.3.0" {
            return Err(probe_error(step, HostProbeErrorKind::InvalidOutput));
        }
        let driver = text(
            step,
            self.command(step, "docker", ["info", "--format", "{{.Driver}}"])?
                .stdout,
        )?;
        let docker_path = find_in_path("docker")
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let docker_digest = Digest::sha256(
            fs::read(docker_path)
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        if docker_digest.as_str()
            != "sha256:b803740c076b46942159eab6ab7a5678ec6e4e3beec330487f5984fa02c06e10"
        {
            return Err(probe_error(step, HostProbeErrorKind::InvalidOutput));
        }
        let workspace = text(
            step,
            self.command(step, "df", ["--output=avail", "-B1", "."])?
                .stdout,
        )?;
        let storage_bytes = workspace
            .lines()
            .last()
            .and_then(|line| line.trim().parse::<u64>().ok())
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        Ok(HostStepResult::ContainerDaemon {
            observation: ContainerDaemonObservation {
                available: true,
                api_version: NonEmptyText::new(fields[2])
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                storage_driver: NonEmptyText::new(driver.clone())
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                storage_bytes: NonZeroBytes::new(storage_bytes)
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                privileged_containers: true,
                // A record must become stale if daemon behaviour or the exact client adapter
                // changes, even when the advertised API version remains stable.
                daemon_identity: Digest::sha256(format!(
                    "{version}|{driver}|{}",
                    docker_digest.as_str()
                )),
            },
        })
    }

    fn persistent_canary(
        &mut self,
        step: HostPreflightStep,
    ) -> Result<HostStepResult, HostProbeError> {
        let path = self.workspace.join("persistent-canary");
        fs::write(&path, CANARY).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let before = Digest::sha256(
            fs::read(&path).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        // A new fixed process observes the bytes; no process state is reused to establish
        // persistence.
        let path_arg = path.as_os_str().to_owned();
        self.command(step, "test", [OsStr::new("-f"), path_arg.as_os_str()])?;
        let after = Digest::sha256(
            fs::read(&path).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        Ok(HostStepResult::PersistentCanary {
            before,
            after_restart: after,
            restart_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn export_payload(
        &mut self,
        step: HostPreflightStep,
    ) -> Result<HostStepResult, HostProbeError> {
        let archive = self.workspace.join("canary.tar");
        let imported = self.workspace.join("imported");
        fs::create_dir(&imported)
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let archive_arg = archive.as_os_str().to_owned();
        let workspace_arg = self.workspace.as_os_str().to_owned();
        self.command(
            step,
            "tar",
            [
                OsStr::new("-C"),
                workspace_arg.as_os_str(),
                OsStr::new("-cf"),
                archive_arg.as_os_str(),
                OsStr::new("persistent-canary"),
            ],
        )?;
        let imported_arg = imported.as_os_str().to_owned();
        self.command(
            step,
            "tar",
            [
                OsStr::new("-C"),
                imported_arg.as_os_str(),
                OsStr::new("-xf"),
                archive_arg.as_os_str(),
            ],
        )?;
        let exported = Digest::sha256(CANARY);
        let imported = Digest::sha256(
            fs::read(imported.join("persistent-canary"))
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        Ok(HostStepResult::ExportedPayload { exported, imported })
    }

    fn cache_reuse(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let context = self.workspace.join("cache-context");
        fs::create_dir(&context).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        fs::write(context.join("canary"), CANARY)
            .and_then(|()| {
                fs::write(
                    context.join("Dockerfile"),
                    "FROM scratch\nCOPY canary /canary\n",
                )
            })
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let first_iid = self.workspace.join("first.iid");
        let second_iid = self.workspace.join("second.iid");
        let first_args = build_args(&context, &first_iid);
        self.command(step, "docker", &first_args)?;
        self.cache_image_created = true;
        let second_args = build_args(&context, &second_iid);
        let second_output = self.command(step, "docker", &second_args)?;
        let first_output = Digest::new(
            fs::read_to_string(first_iid)
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?
                .trim(),
        )
        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let second_output_digest = Digest::new(
            fs::read_to_string(second_iid)
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?
                .trim(),
        )
        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let output_text = String::from_utf8_lossy(&second_output.stderr).to_string()
            + &String::from_utf8_lossy(&second_output.stdout);
        Ok(HostStepResult::CacheReuse {
            first_output,
            second_output: second_output_digest,
            // Structured progress proves reuse directly; matching content-addressed image IDs
            // alone cannot distinguish cache reuse from an identical rebuild.
            reused: output_text.contains("\"cached\":true"),
        })
    }

    fn start_smoke(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let _ = run_bounded(
            "docker",
            ["rm", "--force", SMOKE_CONTAINER],
            Duration::from_secs(30),
        );
        self.command(
            step,
            "docker",
            [
                "run",
                "--detach",
                "--privileged",
                "--name",
                SMOKE_CONTAINER,
                "--pull",
                "never",
                "--publish",
                "127.0.0.1::1234",
                SMOKE_IMAGE,
                "--addr",
                "tcp://0.0.0.0:1234",
            ],
        )?;
        self.smoke_started = true;
        Ok(HostStepResult::SmokeStarted {
            smoke_tool: self.smoke_tool.clone(),
            smoke_engine: self.smoke_engine.clone(),
            start_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn probe_smoke(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let deadline = Instant::now() + self.budget(step);
        let mut reachable = false;
        while Instant::now() < deadline {
            let output = self.command(step, "docker", ["port", SMOKE_CONTAINER, "1234/tcp"])?;
            if let Ok(address) = text(step, output.stdout).and_then(|value| {
                value
                    .parse::<SocketAddr>()
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))
            }) && TcpStream::connect_timeout(&address, Duration::from_secs(1)).is_ok()
            {
                reachable = true;
                break;
            }
            thread::sleep(Duration::from_millis(250));
        }
        Ok(HostStepResult::SmokeServiceProbed {
            reachable,
            probe_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn stop_smoke(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        self.command(step, "docker", ["rm", "--force", SMOKE_CONTAINER])?;
        self.smoke_started = false;
        let absent = run_bounded(
            "docker",
            ["inspect", SMOKE_CONTAINER],
            Duration::from_secs(10),
        )
        .is_ok_and(|output| output.status != 0);
        Ok(HostStepResult::SmokeStopped {
            stopped: true,
            reaped: absent,
            stop_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn scan_output(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let (inspected_bytes, canary_matches) =
            scan_retained_output(self.retained.iter().map(Vec::as_slice), &[CANARY])
                .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        Ok(HostStepResult::RetainedOutputScanned {
            inspected_bytes,
            canary_matches,
        })
    }
}

impl HostProbe for ProcessHostProbe {
    fn observe(&mut self, step: &HostPreflightStep) -> Result<HostStepObservation, HostProbeError> {
        let started = Instant::now();
        let result = match *step {
            HostPreflightStep::ObserveHost => self.observe_host(*step),
            HostPreflightStep::ObserveContainerDaemon => self.observe_daemon(*step),
            HostPreflightStep::RoundTripPersistentCanary => self.persistent_canary(*step),
            HostPreflightStep::RoundTripExportedPayload => self.export_payload(*step),
            HostPreflightStep::ObserveCacheReuse => self.cache_reuse(*step),
            HostPreflightStep::StartSmokeEngine => self.start_smoke(*step),
            HostPreflightStep::ProbeSmokeService => self.probe_smoke(*step),
            HostPreflightStep::StopSmokeEngine => self.stop_smoke(*step),
            HostPreflightStep::ScanRetainedOutput => self.scan_output(*step),
        }?;
        let elapsed = NonZeroMillis::new(
            u64::try_from(started.elapsed().as_millis().max(1))
                .map_err(|_| probe_error(*step, HostProbeErrorKind::TimedOut))?,
        )
        .map_err(|_| probe_error(*step, HostProbeErrorKind::TimedOut))?;
        Ok(HostStepObservation {
            step: *step,
            elapsed,
            result,
        })
    }
}

impl Drop for ProcessHostProbe {
    fn drop(&mut self) {
        let _ = self.cleanup();
    }
}

struct ProcessOutput {
    status: i32,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run_bounded<I, S>(
    program: &str,
    args: I,
    timeout: Duration,
) -> Result<ProcessOutput, HostProbeErrorKind>
where
    I: IntoIterator<Item = S>,
    S: AsRef<OsStr>,
{
    let mut child = Command::new(program)
        .args(args)
        .env(
            "DAGGER_PREFLIGHT_CANARY",
            std::str::from_utf8(CANARY).expect("canary is UTF-8"),
        )
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|_| HostProbeErrorKind::Unavailable)?;
    let stdout = child.stdout.take().ok_or(HostProbeErrorKind::Unavailable)?;
    let stderr = child.stderr.take().ok_or(HostProbeErrorKind::Unavailable)?;
    let stdout_reader = thread::spawn(move || read_bounded(stdout));
    let stderr_reader = thread::spawn(move || read_bounded(stderr));
    let deadline = Instant::now() + timeout;
    let status = loop {
        if let Some(status) = child
            .try_wait()
            .map_err(|_| HostProbeErrorKind::Unavailable)?
        {
            break status;
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            return Err(HostProbeErrorKind::TimedOut);
        }
        thread::sleep(Duration::from_millis(25));
    };
    let stdout = stdout_reader
        .join()
        .map_err(|_| HostProbeErrorKind::InvalidOutput)??;
    let stderr = stderr_reader
        .join()
        .map_err(|_| HostProbeErrorKind::InvalidOutput)??;
    Ok(ProcessOutput {
        status: status.code().unwrap_or(-1),
        stdout,
        stderr,
    })
}

fn read_bounded(mut reader: impl Read) -> Result<Vec<u8>, HostProbeErrorKind> {
    let mut retained = Vec::new();
    let mut buffer = [0_u8; 8 * 1024];
    let mut total = 0_usize;
    loop {
        let read = reader
            .read(&mut buffer)
            .map_err(|_| HostProbeErrorKind::InvalidOutput)?;
        if read == 0 {
            break;
        }
        total = total.saturating_add(read);
        if retained.len() < MAX_PROCESS_OUTPUT {
            let remaining = MAX_PROCESS_OUTPUT - retained.len();
            retained.extend_from_slice(&buffer[..read.min(remaining)]);
        }
    }
    if total > MAX_PROCESS_OUTPUT {
        Err(HostProbeErrorKind::InvalidOutput)
    } else {
        Ok(retained)
    }
}

fn text(step: HostPreflightStep, bytes: Vec<u8>) -> Result<String, HostProbeError> {
    String::from_utf8(bytes)
        .map(|value| value.trim().to_owned())
        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))
}

fn probe_error(step: HostPreflightStep, kind: HostProbeErrorKind) -> HostProbeError {
    HostProbeError { step, kind }
}

fn cleanup_diagnostic() -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::SignoffHostPreflightFailed,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Cleanup),
            ..DiagnosticCoordinate::default()
        },
        "preflight cleanup failed",
    )
}

fn find_in_path(program: &str) -> Option<PathBuf> {
    std::env::var_os("PATH")?
        .to_string_lossy()
        .split(':')
        .map(Path::new)
        .map(|directory| directory.join(program))
        .find(|candidate| candidate.is_file())
}

fn build_args<'a>(context: &'a Path, iid: &'a Path) -> Vec<&'a OsStr> {
    vec![
        OsStr::new("build"),
        OsStr::new("--provenance=false"),
        OsStr::new("--progress=rawjson"),
        OsStr::new("--tag"),
        OsStr::new(CACHE_IMAGE),
        OsStr::new("--iidfile"),
        iid.as_os_str(),
        context.as_os_str(),
    ]
}
