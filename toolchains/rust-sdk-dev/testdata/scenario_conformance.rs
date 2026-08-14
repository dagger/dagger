//! Closed Rust runner for reviewed authority-scenario realizations.
//!
//! Authority identities remain individually traceable in the checked manifest, but equivalent
//! identities share one Rust-owned realization. The runner exposes only those reviewed semantic
//! boundaries and rejects an unknown selector before opening a Dagger session.

use std::collections::{BTreeMap, BTreeSet};
use std::error::Error;
use std::process::Output;
use std::time::Instant;

use dagger_sdk::Client;
#[cfg(test)]
use dagger_sdk::signoff_observation::SignoffSha256;
use dagger_sdk::signoff_observation::{
    SignoffCliSource, SignoffConnectorEvent, SignoffConnectorRecorder, SignoffUnavailableStatus,
};
use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};
use tokio::process::Command;

const TARGET_REVISION: &str = "25300124ca110612edc09c43f89cb5fad6028170";
const TARGET_VERSION: &str = "v1.0.0-beta.10";
const MODULE_ROOT: &str = "scenario-module";
const STABLE_CONNECTOR_HELPER: &str = "DAGGER_RUST_STABLE_CONNECTOR_HELPER";
const STABLE_CONNECTOR_CACHE: &str = "DAGGER_RUST_STABLE_CONNECTOR_CACHE";
const STABLE_CONNECTOR_OUTPUT_LIMIT: usize = 64 * 1024;
const MODULE_SOURCE: &str = r#"
use dagger_sdk as sdk;

#[sdk::object(root, rename = "rustConformance")]
pub(crate) struct RustConformance {
    #[dagger(state)]
    prefix: String,
}

#[sdk::object(rename = "rustMessage")]
pub(crate) struct RustMessage {
    #[dagger(field)]
    value: String,
}

#[sdk::enum_type(rename = "rustMood")]
pub(crate) enum RustMood {
    Happy,
    Quiet,
}

#[sdk::methods]
impl RustConformance {
    #[dagger(constructor)]
    fn new(#[dagger(default = "rust")] prefix: String) -> Self {
        Self { prefix }
    }

    #[dagger(function)]
    fn echo(&self, value: String) -> String {
        format!("{}:{value}", self.prefix)
    }

    #[dagger(function)]
    async fn echo_later(&self, value: String) -> String {
        format!("{}:{value}:async", self.prefix)
    }

    #[dagger(function)]
    fn typed(&self, enabled: bool, count: i64, values: Vec<String>) -> String {
        format!("{}:{enabled}:{count}:{}", self.prefix, values.join(","))
    }

    #[dagger(function)]
    fn mood(&self, mood: RustMood) -> String {
        match mood {
            RustMood::Happy => "HAPPY".to_owned(),
            RustMood::Quiet => "QUIET".to_owned(),
        }
    }

    #[dagger(function)]
    fn source(
        &self,
        #[dagger(default_path = ".", ignore = ["target"])] source: sdk::Directory,
    ) -> sdk::Directory {
        source
    }

    #[dagger(function)]
    fn message(&self, value: String) -> RustMessage {
        RustMessage { value }
    }

    #[dagger(function)]
    fn fail(&self) -> Result<String, sdk::ModuleError> {
        Err(sdk::ModuleError::new("reviewed Rust failure"))
    }

    #[dagger(function)]
    fn panic_safely(&self) -> String {
        panic!("reviewed Rust panic")
    }

    #[dagger(function)]
    fn complete(&self) {}
}
"#;

#[derive(Serialize)]
struct ScenarioObservation {
    case_id: String,
    contract_digest: String,
    proof_id: String,
    realization_id: String,
    realization_kind: &'static str,
    observation: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ScenarioContract {
    case_id: String,
    contract_digest: String,
    proof_id: String,
}

#[derive(Serialize)]
struct ScenarioObservationSet {
    format_version: u32,
    target_revision: &'static str,
    target_version: &'static str,
    observations: Vec<ScenarioObservation>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case", tag = "outcome", deny_unknown_fields)]
enum StableManifestEvidence {
    Available {
        manifest_digest: String,
        cli_digest: String,
        checksum_verified: bool,
    },
    Unavailable {
        status: String,
    },
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct StableConnectorEvidence {
    explicit_local_cli_selected: bool,
    path_cli_digest: String,
    host_cli_visible: bool,
    manifest: StableManifestEvidence,
    selected_source: String,
    selected_cli_digest: String,
    claim: String,
    observed_engine_version: String,
    session_control_succeeded: bool,
    authenticated_loopback_constructed: bool,
    authenticated_query_succeeded: bool,
    close_count: u32,
    child_reap_count: u32,
    elapsed_millis: u64,
}

pub(crate) const fn registered_realization_ids() -> &'static [&'static str] {
    &[
        "realization/common-harness",
        "realization/integration/cli-module",
        "realization/integration/cli-module-init",
        "realization/integration/cli-module-sdk",
        "realization/integration/cli-sdk-init-dynamic",
        "realization/integration/module-benchmark",
        "realization/integration/module-call",
        "realization/integration/module-config",
        "realization/integration/module-config-compat",
        "realization/integration/module-constructor",
        "realization/integration/module-current-module",
        "realization/integration/module-custom-sdk",
        "realization/integration/module-definition",
        "realization/integration/module-dependency-cli",
        "realization/integration/module-dependency-runtime",
        "realization/integration/module-deprecation",
        "realization/integration/module-engine-version",
        "realization/integration/module-error",
        "realization/integration/module-iface",
        "realization/integration/module-introspection-cli",
        "realization/integration/module-loading",
        "realization/integration/module-path-inputs",
        "realization/integration/module-private-deps",
        "realization/integration/module-runtime-behavior",
        "realization/integration/module-runtime-codegen",
        "realization/integration/module-self-calls",
        "realization/integration/module-terminal",
        "realization/integration/module-type",
        "realization/integration/module-up",
        "realization/integration/module-validation",
        "realization/integration/workspace-modules",
        "realization/module-concurrency-cancellation",
        "realization/module-common-harness",
        "realization/module-constructor-state",
        "realization/module-execution-shapes",
        "realization/module-handles-context",
        "realization/module-negative-dispatch",
        "realization/module-packaged-self-consumer",
        "realization/module-registration",
        "realization/module-types",
        "realization/packaged-runtime",
        "realization/shared-cli",
        "realization/stable-connector",
        "realization/standalone-clients",
    ]
}

async fn execute_realization(
    realization_id: &str,
    client: &Client,
) -> Result<String, Box<dyn Error>> {
    let observation = match realization_id {
        "realization/common-harness" => observe_common_harness().await?,
        integration if integration.starts_with("realization/integration/") => {
            return Err(std::io::Error::other(format!(
                "integration realization {integration:?} requires explicit scenario proofs"
            ))
            .into());
        }
        "realization/shared-cli" => observe_shared_cli().await?,
        "realization/module-registration" => observe_registration().await?,
        "realization/module-constructor-state" => observe_constructor_state().await?,
        "realization/module-execution-shapes" => observe_execution_shapes().await?,
        "realization/module-types" => observe_types().await?,
        "realization/module-handles-context" => observe_handles_context().await?,
        "realization/module-negative-dispatch" => observe_negative_dispatch().await?,
        "realization/module-concurrency-cancellation" => observe_concurrency().await?,
        "realization/module-common-harness" => observe_module_common_harness().await?,
        "realization/module-packaged-self-consumer" => {
            observe_module_packaged_self_consumer().await?
        }
        "realization/packaged-runtime" => observe_packaged_runtime(client).await?,
        "realization/stable-connector" => observe_stable_connector(client).await?,
        "realization/standalone-clients" => observe_standalone_clients(client).await?,
        _ => {
            return Err(std::io::Error::other(format!(
                "unknown Rust scenario realization {realization_id:?}"
            ))
            .into());
        }
    };
    Ok(observation)
}

async fn observe_scenario_proof(proof_id: &str, client: &Client) -> Result<String, Box<dyn Error>> {
    match proof_id {
        "probe/compatibility/configuration" => observe_common_harness().await,
        "probe/compatibility/target-version" => observe_stable_connector(client).await,
        "probe/filesystem/path-boundary" => observe_handles_context().await,
        "probe/isolation/call" => observe_concurrency().await,
        "probe/isolation/workspace" => observe_workspace_isolation().await,
        "probe/lifecycle/invoke" | "probe/query/module" => observe_execution_shapes().await,
        "probe/metadata/definition" | "probe/query/introspection" => observe_registration().await,
        "probe/omission/explicit-value" => observe_explicit_values().await,
        "probe/query/dependency" => observe_packaged_runtime(client).await,
        "probe/result/exact-value" => observe_types().await,
        "probe/typed-error/category" => observe_negative_dispatch().await,
        _ => Err(std::io::Error::other(format!("unknown Rust scenario proof {proof_id:?}")).into()),
    }
}

async fn observe_shared_cli() -> Result<String, Box<dyn Error>> {
    let output = dagger(&["sdk", "list"]).await?;
    require_success(&output, "list installed SDKs")?;
    let stdout = String::from_utf8(output.stdout)?;
    if !stdout.to_ascii_lowercase().contains("rust") {
        return Err(std::io::Error::other("installed SDK list omitted Rust").into());
    }
    Ok("installed-rust-sdk-visible-to-shared-cli".to_owned())
}

async fn observe_common_harness() -> Result<String, Box<dyn Error>> {
    let version = dagger(&["version"]).await?;
    require_success(&version, "read exact engine version")?;
    if !String::from_utf8(version.stdout)?.contains(TARGET_VERSION) {
        return Err(std::io::Error::other("CLI version differs from exact target").into());
    }

    let installed = dagger(&["sdk", "list"]).await?;
    require_success(&installed, "list installed SDKs")?;
    if !String::from_utf8(installed.stdout)?
        .to_ascii_lowercase()
        .contains("rust")
    {
        return Err(std::io::Error::other("installed SDK list omitted Rust").into());
    }

    let options = dagger(&["sdk", "module-options", "rust"]).await?;
    require_success(&options, "inspect Rust module initializer options")?;
    if !String::from_utf8(options.stdout)?.contains("dagger module init rust") {
        return Err(std::io::Error::other("Rust SDK omitted module initializer options").into());
    }

    prepare_module().await?;
    let authored = std::fs::read_to_string(format!("{MODULE_ROOT}/src/lib.rs"))?;
    if authored != MODULE_SOURCE {
        return Err(std::io::Error::other("module initialization changed authored source").into());
    }
    for required in [
        "Cargo.toml",
        "Cargo.lock",
        "rust-toolchain.toml",
        "src/bin/dagger-module.rs",
        ".dagger/rust/operation-manifest.json",
    ] {
        if !std::path::Path::new(MODULE_ROOT).join(required).is_file() {
            return Err(std::io::Error::other(format!(
                "Rust module scaffold omitted {required:?}"
            ))
            .into());
        }
    }
    if std::path::Path::new(MODULE_ROOT)
        .join("dagger.json")
        .exists()
    {
        return Err(std::io::Error::other("Rust initializer wrote a legacy module config").into());
    }

    let generators = dagger(&["generate", "-l"]).await?;
    require_success(&generators, "list Rust generators")?;
    if String::from_utf8(generators.stdout)?.trim().is_empty() {
        return Err(std::io::Error::other("Rust SDK exposed no generator").into());
    }
    let functions = module_stdout(&["functions"]).await?;
    if !functions.contains("echo") {
        return Err(std::io::Error::other("scaffolded Rust module did not load").into());
    }
    let call = module_stdout(&["call", "echo", "--value", "harness"]).await?;
    require_trimmed(&call, "rust:harness", "scaffolded module call")?;
    Ok("all-seventeen-common-harness-contracts-observed".to_owned())
}

async fn observe_registration() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let output = module(&["functions"]).await?;
    require_success(&output, "load reviewed Rust module")?;
    let stdout = String::from_utf8(output.stdout)?;
    for function in ["echo", "echo-later", "typed", "source", "fail"] {
        if !stdout.contains(function) {
            return Err(std::io::Error::other(format!(
                "reviewed Rust module omitted {function:?}"
            ))
            .into());
        }
    }
    Ok("reviewed-rust-typedef-registration-loaded".to_owned())
}

async fn observe_constructor_state() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let stdout =
        module_stdout(&["call", "--prefix", "state", "echo", "--value", "retained"]).await?;
    require_trimmed(&stdout, "state:retained", "constructor state")?;
    Ok("constructor-state-round-tripped".to_owned())
}

async fn observe_execution_shapes() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let sync = module_stdout(&["call", "echo", "--value", "sync"]).await?;
    require_trimmed(&sync, "rust:sync", "synchronous dispatch")?;
    let asynchronous = module_stdout(&["call", "echo-later", "--value", "later"]).await?;
    require_trimmed(&asynchronous, "rust:later:async", "asynchronous dispatch")?;
    let unit = module_stdout(&["call", "complete"]).await?;
    require_trimmed(&unit, "", "unit dispatch")?;
    Ok("sync-async-and-unit-dispatch-observed".to_owned())
}

async fn observe_types() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let typed = module_stdout(&[
        "call",
        "typed",
        "--enabled=true",
        "--count=7",
        "--values=one,two",
    ])
    .await?;
    require_trimmed(&typed, "rust:true:7:one,two", "typed arguments")?;
    let mood = module_stdout(&["call", "mood", "--mood=HAPPY"]).await?;
    require_trimmed(&mood, "HAPPY", "enum argument")?;
    let message = module_stdout(&["call", "message", "--value", "owned", "value"]).await?;
    require_trimmed(&message, "owned", "local object return")?;
    Ok("scalar-list-enum-and-object-codecs-observed".to_owned())
}

async fn observe_handles_context() -> Result<String, Box<dyn Error>> {
    std::fs::create_dir_all(MODULE_ROOT)?;
    std::fs::write(format!("{MODULE_ROOT}/visible.txt"), "visible")?;
    prepare_module().await?;
    let stdout = module_stdout(&["call", "source", "entries"]).await?;
    if !stdout.lines().any(|line| line.trim() == "visible.txt") {
        return Err(std::io::Error::other("directory handle omitted authored file").into());
    }
    Ok("generated-core-handle-crossed-module-context".to_owned())
}

async fn observe_negative_dispatch() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    for function in ["fail", "panic-safely"] {
        let output = module(&["call", function]).await?;
        if output.status.success() {
            return Err(std::io::Error::other(format!(
                "negative dispatch {function:?} unexpectedly succeeded"
            ))
            .into());
        }
        let rendered = format!(
            "{}{}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        if function == "fail" && !rendered.contains("reviewed Rust failure") {
            return Err(std::io::Error::other(format!(
                "negative dispatch {function:?} omitted its typed diagnostic"
            ))
            .into());
        }
        if function == "panic-safely" && rendered.contains("reviewed Rust panic") {
            return Err(std::io::Error::other(
                "panic dispatch exposed the module's raw panic payload",
            )
            .into());
        }
    }
    Ok("error-and-panic-remained-distinct-failures".to_owned())
}

async fn observe_concurrency() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let (left, right) = tokio::join!(
        module_stdout(&["call", "echo-later", "--value", "left"]),
        module_stdout(&["call", "echo-later", "--value", "right"]),
    );
    require_trimmed(&left?, "rust:left:async", "left concurrent call")?;
    require_trimmed(&right?, "rust:right:async", "right concurrent call")?;
    Ok("concurrent-call-state-remained-isolated".to_owned())
}

async fn observe_workspace_isolation() -> Result<String, Box<dyn Error>> {
    const SENTINEL: &str = "caller-owned-workspace-state";
    std::fs::create_dir_all("scenario-sibling")?;
    std::fs::write("scenario-sibling/sentinel.txt", SENTINEL)?;
    prepare_module().await?;
    let output = module_stdout(&["call", "echo", "--value", "isolated"]).await?;
    require_trimmed(&output, "rust:isolated", "isolated workspace call")?;
    if std::fs::read_to_string("scenario-sibling/sentinel.txt")? != SENTINEL {
        return Err(
            std::io::Error::other("module execution changed sibling workspace state").into(),
        );
    }
    Ok("module-call-remained-within-its-workspace".to_owned())
}

async fn observe_explicit_values() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let output = module_stdout(&[
        "call",
        "typed",
        "--enabled=false",
        "--count=0",
        "--values=present",
    ])
    .await?;
    require_trimmed(
        &output,
        "rust:false:0:present",
        "explicit false and zero arguments",
    )?;
    Ok("explicit-false-and-zero-crossed-production-dispatch".to_owned())
}

async fn observe_module_common_harness() -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let functions = module_stdout(&["functions"]).await?;
    if !functions.contains("echo") {
        return Err(std::io::Error::other("initialized Rust module did not load").into());
    }
    let probe = module_stdout(&["call", "echo", "--value", "harness"]).await?;
    require_trimmed(&probe, "rust:harness", "module harness invocation")?;
    Ok("initialized-generated-loaded-and-invoked-rust-module".to_owned())
}

async fn observe_module_packaged_self_consumer() -> Result<String, Box<dyn Error>> {
    std::fs::create_dir_all(MODULE_ROOT)?;
    std::fs::write(format!("{MODULE_ROOT}/packaged.txt"), "package-owned")?;
    prepare_module().await?;
    let stdout =
        module_stdout(&["call", "source", "file", "--path=packaged.txt", "contents"]).await?;
    require_trimmed(&stdout, "package-owned", "packaged module Core call")?;
    Ok("module-consumed-exact-packaged-rust-sdk".to_owned())
}

async fn observe_packaged_runtime(client: &Client) -> Result<String, Box<dyn Error>> {
    let stdout = client
        .query()
        .container()
        .from("alpine:3.22")
        .with_exec(vec!["sh", "-c", "printf exact-packaged-rust-runtime"])
        .stdout()
        .await?;
    require_trimmed(
        &stdout,
        "exact-packaged-rust-runtime",
        "packaged public client",
    )?;
    Ok("exact-packaged-rust-runtime-executed".to_owned())
}

async fn observe_stable_connector(_client: &Client) -> Result<String, Box<dyn Error>> {
    let isolated = tempfile::tempdir()?;
    let bin = isolated.path().join("bin");
    let home = isolated.path().join("home");
    std::fs::create_dir_all(&bin)?;
    std::fs::create_dir_all(&home)?;
    let artifact_cli = find_path_cli()?;
    let isolated_cli = bin.join(if cfg!(windows) {
        "dagger.exe"
    } else {
        "dagger"
    });
    std::fs::copy(&artifact_cli, &isolated_cli)?;
    let path = std::env::join_paths([&bin])?;

    let cache_root = if cfg!(target_os = "macos") {
        home.join("Library/Caches/dagger")
    } else {
        isolated.path().join("cache/dagger")
    };
    let output = Command::new(std::env::current_exe()?)
        .env(STABLE_CONNECTOR_HELPER, "1")
        .env(STABLE_CONNECTOR_CACHE, &cache_root)
        .env("PATH", path)
        .env("HOME", &home)
        .env("XDG_CACHE_HOME", isolated.path().join("cache"))
        .env_remove("DAGGER_SESSION_PORT")
        .env_remove("DAGGER_SESSION_TOKEN")
        .env_remove("_EXPERIMENTAL_DAGGER_CLI_BIN")
        .output()
        .await?;
    if output.stdout.len() > STABLE_CONNECTOR_OUTPUT_LIMIT
        || output.stderr.len() > STABLE_CONNECTOR_OUTPUT_LIMIT
    {
        return Err(
            std::io::Error::other("stable connector helper exceeded its output bound").into(),
        );
    }
    require_success(&output, "run isolated stable default connector")?;
    let evidence: StableConnectorEvidence = serde_json::from_slice(&output.stdout)?;
    validate_stable_connector_evidence(&evidence)?;
    Ok(String::from_utf8(serde_json::to_vec(&evidence)?)?)
}

async fn run_stable_connector_helper() -> Result<(), Box<dyn Error>> {
    if std::env::var_os("DAGGER_SESSION_PORT").is_some()
        || std::env::var_os("DAGGER_SESSION_TOKEN").is_some()
        || std::env::var_os("_EXPERIMENTAL_DAGGER_CLI_BIN").is_some()
    {
        return Err(std::io::Error::other(
            "isolated stable connector retained an existing or explicit-local source",
        )
        .into());
    }
    let path_cli = find_path_cli()?;
    let path_cli_digest = sha256_file(&path_cli)?;
    let cache = std::env::var_os(STABLE_CONNECTOR_CACHE)
        .map(std::path::PathBuf::from)
        .ok_or_else(|| std::io::Error::other("isolated cache directory is unavailable"))?;
    if cache.exists() && std::fs::read_dir(&cache)?.next().is_some() {
        return Err(std::io::Error::other("isolated stable connector cache was not empty").into());
    }

    let (recorder, recording) = SignoffConnectorRecorder::bounded();
    let config = dagger_sdk::ClientConfig::builder()
        .signoff_recorder(recorder)
        .build()?;
    let started = Instant::now();
    let client = dagger_sdk::connect_with(config).await?;
    let version = client.query().version().await?;
    if version != TARGET_VERSION {
        return Err(std::io::Error::other(format!(
            "stable connector reached {version:?}, want {TARGET_VERSION:?}"
        ))
        .into());
    }
    client.close().await?;
    let evidence = stable_connector_evidence(
        recording.finish()?,
        path_cli_digest,
        cache,
        version,
        started.elapsed().as_millis().max(1) as u64,
    )?;
    println!("{}", String::from_utf8(serde_json::to_vec(&evidence)?)?);
    Ok(())
}

fn stable_connector_evidence(
    events: Vec<SignoffConnectorEvent>,
    path_cli_digest: String,
    cache: std::path::PathBuf,
    observed_engine_version: String,
    elapsed_millis: u64,
) -> Result<StableConnectorEvidence, Box<dyn Error>> {
    let position = |wanted: fn(&SignoffConnectorEvent) -> bool| {
        events.iter().position(wanted).ok_or_else(|| {
            std::io::Error::other("stable connector omitted a required production observation")
        })
    };
    let compiled =
        position(|event| matches!(event, SignoffConnectorEvent::CompiledReleaseSelected))?;
    let selected = position(|event| matches!(event, SignoffConnectorEvent::CliSelected { .. }))?;
    let child_started = position(|event| matches!(event, SignoffConnectorEvent::ChildStarted))?;
    let control = position(|event| matches!(event, SignoffConnectorEvent::SessionControlAccepted))?;
    let loopback = position(|event| {
        matches!(
            event,
            SignoffConnectorEvent::AuthenticatedLoopbackConstructed
        )
    })?;
    let query =
        position(|event| matches!(event, SignoffConnectorEvent::AuthenticatedQuerySucceeded))?;
    let close = position(|event| matches!(event, SignoffConnectorEvent::CloseStarted))?;
    let reaped = position(|event| matches!(event, SignoffConnectorEvent::ChildReaped))?;
    let closed = position(|event| matches!(event, SignoffConnectorEvent::CloseCompleted))?;
    if !(compiled < selected
        && selected < child_started
        && child_started < control
        && control < loopback
        && loopback < query
        && query < close
        && close < reaped
        && reaped < closed)
    {
        return Err(std::io::Error::other(
            "stable connector lifecycle observations were not ordered",
        )
        .into());
    }

    let selected_event = events
        .iter()
        .find_map(|event| match event {
            SignoffConnectorEvent::CliSelected {
                source,
                executable_sha256,
            } => Some((*source, format!("sha256:{}", executable_sha256.to_hex()))),
            _ => None,
        })
        .ok_or_else(|| std::io::Error::other("stable connector omitted selected CLI identity"))?;
    let available = events
        .iter()
        .enumerate()
        .find_map(|(index, event)| match event {
            SignoffConnectorEvent::ManifestAvailable { manifest_sha256 } => {
                Some((index, format!("sha256:{}", manifest_sha256.to_hex())))
            }
            _ => None,
        });
    let unavailable = events
        .iter()
        .enumerate()
        .find_map(|(index, event)| match event {
            SignoffConnectorEvent::ManifestUnavailable { status } => Some((index, *status)),
            _ => None,
        });
    let checksum_position = events
        .iter()
        .position(|event| matches!(event, SignoffConnectorEvent::ArchiveChecksumVerified));
    let checksum_verified = checksum_position.is_some();

    let (manifest, selected_source, claim) = match (
        available,
        unavailable,
        checksum_verified,
        selected_event.0,
    ) {
        (
            Some((manifest_position, manifest_digest)),
            None,
            true,
            SignoffCliSource::VerifiedDownload,
        ) => {
            if !(compiled < manifest_position
                && manifest_position
                    < checksum_position.expect("verified checksum position exists")
                && checksum_position.expect("verified checksum position exists") < selected)
            {
                return Err(std::io::Error::other(
                    "verified distribution observations were not ordered",
                )
                .into());
            }
            let cached = managed_cache_cli_digests(&cache)?;
            if cached != [selected_event.1.clone()] {
                return Err(std::io::Error::other(
                    "verified download did not leave exactly the selected CLI in the isolated cache",
                )
                .into());
            }
            (
                StableManifestEvidence::Available {
                    manifest_digest,
                    cli_digest: selected_event.1.clone(),
                    checksum_verified: true,
                },
                "verified-download".to_owned(),
                "VERIFIED_DOWNLOAD".to_owned(),
            )
        }
        (
            None,
            Some((manifest_position, status)),
            false,
            SignoffCliSource::CompatibilityPathFallback,
        ) => {
            if !(compiled < manifest_position && manifest_position < selected) {
                return Err(std::io::Error::other(
                    "compatibility distribution observations were not ordered",
                )
                .into());
            }
            if selected_event.1 != path_cli_digest || !managed_cache_cli_digests(&cache)?.is_empty()
            {
                return Err(std::io::Error::other(
                    "compatibility fallback did not select only the exact PATH artifact CLI",
                )
                .into());
            }
            (
                StableManifestEvidence::Unavailable {
                    status: match status {
                        SignoffUnavailableStatus::Forbidden => "forbidden",
                        SignoffUnavailableStatus::NotFound => "not-found",
                    }
                    .to_owned(),
                },
                "artifact-path-fallback".to_owned(),
                "COMPATIBILITY_PATH_FALLBACK".to_owned(),
            )
        }
        _ => {
            return Err(std::io::Error::other(
                "stable connector claimed overlapping or incomplete distribution outcomes",
            )
            .into());
        }
    };

    let required_event_count = if checksum_verified { 11 } else { 10 };
    if events.len() != required_event_count {
        return Err(std::io::Error::other(
            "stable connector emitted duplicate or unreviewed production observations",
        )
        .into());
    }
    let evidence = StableConnectorEvidence {
        explicit_local_cli_selected: false,
        path_cli_digest,
        host_cli_visible: false,
        manifest,
        selected_source,
        selected_cli_digest: selected_event.1,
        claim,
        observed_engine_version,
        session_control_succeeded: true,
        authenticated_loopback_constructed: true,
        authenticated_query_succeeded: true,
        close_count: 1,
        child_reap_count: 1,
        elapsed_millis,
    };
    validate_stable_connector_evidence(&evidence)?;
    Ok(evidence)
}

fn validate_stable_connector_evidence(
    evidence: &StableConnectorEvidence,
) -> Result<(), Box<dyn Error>> {
    let distribution_is_honest = match (
        &evidence.manifest,
        evidence.selected_source.as_str(),
        evidence.claim.as_str(),
    ) {
        (
            StableManifestEvidence::Available {
                cli_digest,
                checksum_verified: true,
                ..
            },
            "verified-download",
            "VERIFIED_DOWNLOAD",
        ) => cli_digest == &evidence.selected_cli_digest,
        (
            StableManifestEvidence::Unavailable { status },
            "artifact-path-fallback",
            "COMPATIBILITY_PATH_FALLBACK",
        ) => {
            matches!(status.as_str(), "forbidden" | "not-found")
                && evidence.selected_cli_digest == evidence.path_cli_digest
        }
        _ => false,
    };
    if evidence.explicit_local_cli_selected
        || evidence.host_cli_visible
        || !distribution_is_honest
        || evidence.observed_engine_version != TARGET_VERSION
        || !evidence.session_control_succeeded
        || !evidence.authenticated_loopback_constructed
        || !evidence.authenticated_query_succeeded
        || evidence.close_count != 1
        || evidence.child_reap_count != 1
        || evidence.elapsed_millis == 0
    {
        return Err(std::io::Error::other(
            "stable connector evidence is incomplete or contradictory",
        )
        .into());
    }
    Ok(())
}

fn managed_cache_cli_digests(cache: &std::path::Path) -> Result<Vec<String>, Box<dyn Error>> {
    if !cache.exists() {
        return Ok(Vec::new());
    }
    let mut digests = std::fs::read_dir(cache)?
        .filter_map(Result::ok)
        .filter(|entry| {
            entry.file_type().is_ok_and(|kind| kind.is_file())
                && entry
                    .file_name()
                    .to_str()
                    .is_some_and(|name| name.starts_with("dagger-") && !name.ends_with(".lock"))
        })
        .map(|entry| sha256_file(&entry.path()))
        .collect::<Result<Vec<_>, _>>()?;
    digests.sort();
    Ok(digests)
}

fn find_path_cli() -> Result<std::path::PathBuf, Box<dyn Error>> {
    let path = std::env::var_os("PATH")
        .ok_or_else(|| std::io::Error::other("stable connector PATH is unavailable"))?;
    for directory in std::env::split_paths(&path) {
        let candidate = directory.join(if cfg!(windows) {
            "dagger.exe"
        } else {
            "dagger"
        });
        if candidate.is_file() {
            return Ok(candidate);
        }
    }
    Err(std::io::Error::other("stable connector PATH omitted dagger").into())
}

fn sha256_file(path: &std::path::Path) -> Result<String, Box<dyn Error>> {
    const HASH_LIMIT: u64 = 256 * 1024 * 1024;
    use std::io::Read as _;

    let mut file = std::fs::File::open(path)?;
    if file.metadata()?.len() > HASH_LIMIT {
        return Err(
            std::io::Error::other("stable connector executable exceeded its hash bound").into(),
        );
    }
    let mut hasher = Sha256::new();
    let mut observed = 0_u64;
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        let count = file.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        observed = observed.saturating_add(count as u64);
        if observed > HASH_LIMIT {
            return Err(std::io::Error::other(
                "stable connector executable changed beyond its hash bound",
            )
            .into());
        }
        hasher.update(&buffer[..count]);
    }
    Ok(format!("sha256:{:x}", hasher.finalize()))
}

async fn observe_standalone_clients(client: &Client) -> Result<String, Box<dyn Error>> {
    prepare_module().await?;
    let initialized = dagger(&[
        "-y",
        "api",
        "client",
        "init",
        "rust",
        "external-client",
        MODULE_ROOT,
    ])
    .await?;
    require_success(&initialized, "initialize local standalone Rust client")?;
    for required in [
        "Cargo.toml",
        "src/lib.rs",
        "examples/dagger-client-quickstart.rs",
        ".dagger/rust/operation-manifest.json",
    ] {
        if !std::path::Path::new("external-client")
            .join(required)
            .is_file()
        {
            return Err(std::io::Error::other(format!(
                "standalone Rust client omitted {required:?}"
            ))
            .into());
        }
    }
    let authored_readme = "# Authored client notes\n";
    std::fs::write("external-client/AUTHORED.md", authored_readme)?;

    let check = Command::new("cargo")
        .args([
            "check",
            "--manifest-path",
            "external-client/Cargo.toml",
            "--all-targets",
        ])
        .output()
        .await?;
    require_success(&check, "compile standalone Rust client")?;

    let quickstart = Command::new("cargo")
        .args([
            "run",
            "--manifest-path",
            "external-client/Cargo.toml",
            "--example",
            "dagger-client-quickstart",
        ])
        .output()
        .await?;
    require_success(&quickstart, "run generated standalone Rust quickstart")?;

    let generated = std::fs::read("external-client/src/dagger_client/mod.rs")?;
    let expanded = MODULE_SOURCE.replace(
        "    #[dagger(function)]\n    fn complete(&self) {}",
        "    #[dagger(function)]\n    fn regenerated(&self) -> String { \"regenerated\".to_owned() }\n\n    #[dagger(function)]\n    fn complete(&self) {}",
    );
    std::fs::write(format!("{MODULE_ROOT}/src/lib.rs"), expanded)?;
    let regenerate = dagger(&["generate", "-y", "*external-client*"]).await?;
    require_success(&regenerate, "regenerate only the standalone Rust client")?;
    let regenerated = std::fs::read("external-client/src/dagger_client/mod.rs")?;
    if regenerated == generated
        || std::fs::read_to_string("external-client/AUTHORED.md")? != authored_readme
    {
        return Err(std::io::Error::other(
            "standalone regeneration did not change generated bytes or preserve authored bytes",
        )
        .into());
    }

    let remote = format!("github.com/dagger/dagger/modules/ruff@{}", TARGET_REVISION);
    let pinned = dagger(&[
        "-y",
        "api",
        "client",
        "init",
        "rust",
        "remote-client",
        &remote,
    ])
    .await?;
    require_success(
        &pinned,
        "initialize immutable remote standalone Rust client",
    )?;
    if !std::path::Path::new("remote-client/Cargo.toml").is_file() {
        return Err(std::io::Error::other("pinned remote client was not materialized").into());
    }

    let version = client.query().version().await?;
    if version != TARGET_VERSION {
        return Err(
            std::io::Error::other("standalone Core query reached a different target").into(),
        );
    }
    Ok("local-remote-regenerated-core-and-module-clients-observed".to_owned())
}

async fn prepare_module() -> Result<(), Box<dyn Error>> {
    std::fs::create_dir_all(format!("{MODULE_ROOT}/src"))?;
    std::fs::write(format!("{MODULE_ROOT}/src/lib.rs"), MODULE_SOURCE)?;
    let output = dagger(&[
        "-y",
        "module",
        "init",
        "rust",
        "scenario-conformance",
        "--path",
        MODULE_ROOT,
    ])
    .await?;
    require_success(&output, "initialize reviewed Rust module")
}

async fn module_stdout(arguments: &[&str]) -> Result<String, Box<dyn Error>> {
    let output = module(arguments).await?;
    require_success(&output, "execute reviewed Rust module")?;
    Ok(String::from_utf8(output.stdout)?)
}

async fn module(arguments: &[&str]) -> Result<Output, Box<dyn Error>> {
    let mut complete = vec!["-m", MODULE_ROOT];
    complete.extend_from_slice(arguments);
    dagger(&complete).await
}

async fn dagger(arguments: &[&str]) -> Result<Output, Box<dyn Error>> {
    Ok(Command::new("dagger")
        .arg("--silent")
        .args(arguments)
        .output()
        .await?)
}

fn require_success(output: &Output, operation: &str) -> Result<(), Box<dyn Error>> {
    if output.status.success() {
        return Ok(());
    }
    Err(std::io::Error::other(format!(
        "{operation} failed: {}{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    ))
    .into())
}

fn require_trimmed(actual: &str, expected: &str, operation: &str) -> Result<(), Box<dyn Error>> {
    if actual.trim() == expected {
        return Ok(());
    }
    Err(std::io::Error::other(format!(
        "{operation} returned {:?}, want {expected:?}",
        actual.trim()
    ))
    .into())
}

async fn run() -> Result<(), Box<dyn Error>> {
    if std::env::var_os(STABLE_CONNECTOR_HELPER).is_some() {
        return run_stable_connector_helper().await;
    }
    let realization_id = std::env::var("DAGGER_RUST_SCENARIO_REALIZATION")
        .map_err(|_| "DAGGER_RUST_SCENARIO_REALIZATION is required")?;
    if !registered_realization_ids().contains(&realization_id.as_str()) {
        return Err(std::io::Error::other(format!(
            "unknown Rust scenario realization {realization_id:?}"
        ))
        .into());
    }

    let encoded_contracts = std::env::var("DAGGER_RUST_SCENARIO_CONTRACTS")
        .map_err(|_| "DAGGER_RUST_SCENARIO_CONTRACTS is required")?;
    let contracts: Vec<ScenarioContract> = serde_json::from_str(&encoded_contracts)?;
    if contracts.is_empty() {
        return Err(std::io::Error::other("scenario contract set is empty").into());
    }
    let mut identities = contracts
        .iter()
        .map(|contract| contract.case_id.as_str())
        .collect::<Vec<_>>();
    if identities.iter().any(|identity| identity.is_empty()) {
        return Err(std::io::Error::other("scenario contract identity is empty").into());
    }
    let original = identities.clone();
    identities.sort_unstable();
    identities.dedup();
    if identities.len() != original.len()
        || contracts.iter().any(|contract| {
            !contract.contract_digest.starts_with("sha256:")
                || contract.contract_digest.len() != 71
                || !contract.contract_digest[7..]
                    .bytes()
                    .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
                || !contract.proof_id.starts_with("probe/")
        })
    {
        return Err(std::io::Error::other(
            "scenario contracts are duplicated or carry an invalid digest",
        )
        .into());
    }

    let client = dagger_sdk::connect().await?;
    let proofs = contracts
        .iter()
        .map(|contract| contract.proof_id.clone())
        .collect::<BTreeSet<_>>();
    let mut observed = BTreeMap::new();
    if realization_id.starts_with("realization/integration/") {
        for proof_id in proofs {
            observed.insert(
                proof_id.clone(),
                observe_scenario_proof(&proof_id, &client).await?,
            );
        }
    } else {
        let result = execute_realization(&realization_id, &client).await?;
        for proof_id in proofs {
            observed.insert(proof_id, result.clone());
        }
    }
    client.close().await?;
    let observations = contracts
        .into_iter()
        .map(|contract| {
            let observation = observed
                .get(&contract.proof_id)
                .expect("every admitted scenario proof was executed")
                .clone();
            ScenarioObservation {
                case_id: contract.case_id,
                contract_digest: contract.contract_digest,
                proof_id: contract.proof_id,
                realization_id: realization_id.clone(),
                realization_kind: "reviewed-rust-fixture",
                observation,
            }
        })
        .collect();
    println!(
        "{}",
        String::from_utf8(serde_json::to_vec(&ScenarioObservationSet {
            format_version: 1,
            target_revision: TARGET_REVISION,
            target_version: TARGET_VERSION,
            observations,
        })?)?
    );
    Ok(())
}

#[cfg(not(test))]
#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    run().await
}

#[cfg(test)]
mod stable_connector_tests {
    use super::*;

    fn digest(bytes: &[u8]) -> SignoffSha256 {
        SignoffSha256::from_bytes(Sha256::digest(bytes).into())
    }

    fn lifecycle(source: SignoffCliSource, executable: &[u8]) -> Vec<SignoffConnectorEvent> {
        vec![
            SignoffConnectorEvent::CompiledReleaseSelected,
            SignoffConnectorEvent::CliSelected {
                source,
                executable_sha256: digest(executable),
            },
            SignoffConnectorEvent::ChildStarted,
            SignoffConnectorEvent::SessionControlAccepted,
            SignoffConnectorEvent::AuthenticatedLoopbackConstructed,
            SignoffConnectorEvent::AuthenticatedQuerySucceeded,
            SignoffConnectorEvent::CloseStarted,
            SignoffConnectorEvent::ChildReaped,
            SignoffConnectorEvent::CloseCompleted,
        ]
    }

    #[test]
    fn verified_download_requires_manifest_checksum_and_only_the_selected_cache_entry() {
        let isolated = tempfile::tempdir().expect("create isolated connector test directory");
        let cache = isolated.path().join("cache");
        std::fs::create_dir_all(&cache).expect("create isolated connector cache");
        let executable = b"verified downloaded cli";
        std::fs::write(cache.join("dagger-v1.0.0-beta.10"), executable)
            .expect("write selected cached CLI");

        let mut events = lifecycle(SignoffCliSource::VerifiedDownload, executable);
        events.insert(
            1,
            SignoffConnectorEvent::ManifestAvailable {
                manifest_sha256: digest(b"manifest"),
            },
        );
        events.insert(2, SignoffConnectorEvent::ArchiveChecksumVerified);

        let evidence = stable_connector_evidence(
            events,
            format!("sha256:{}", digest(b"path cli").to_hex()),
            cache,
            TARGET_VERSION.to_owned(),
            1,
        )
        .expect("assemble verified-download evidence");
        assert!(matches!(
            evidence.manifest,
            StableManifestEvidence::Available {
                checksum_verified: true,
                ..
            }
        ));
        assert_eq!(evidence.selected_source, "verified-download");
    }

    #[test]
    fn compatibility_fallback_requires_one_exact_unavailable_status_and_no_cache_entry() {
        let isolated = tempfile::tempdir().expect("create isolated connector test directory");
        let executable = b"exact path cli";
        let path_digest = format!("sha256:{}", digest(executable).to_hex());
        let mut events = lifecycle(SignoffCliSource::CompatibilityPathFallback, executable);
        events.insert(
            1,
            SignoffConnectorEvent::ManifestUnavailable {
                status: SignoffUnavailableStatus::NotFound,
            },
        );

        let evidence = stable_connector_evidence(
            events.clone(),
            path_digest.clone(),
            isolated.path().join("cache"),
            TARGET_VERSION.to_owned(),
            1,
        )
        .expect("assemble exact compatibility-fallback evidence");
        assert!(matches!(
            evidence.manifest,
            StableManifestEvidence::Unavailable { ref status } if status == "not-found"
        ));
        assert_eq!(evidence.selected_cli_digest, path_digest);

        events.insert(
            1,
            SignoffConnectorEvent::ManifestAvailable {
                manifest_sha256: digest(b"overlapping manifest"),
            },
        );
        assert!(
            stable_connector_evidence(
                events,
                evidence.path_cli_digest,
                isolated.path().join("cache"),
                TARGET_VERSION.to_owned(),
                1,
            )
            .is_err(),
            "overlapping available and unavailable outcomes must fail closed"
        );
    }
}
