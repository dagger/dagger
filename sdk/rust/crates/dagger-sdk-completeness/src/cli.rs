//! Thin command-line parsing, output separation, and exit policy.
//!
//! Command handlers own contract orchestration; this module owns only the reviewed argv surface,
//! staging boundary, report projection, diagnostics stream, and exit semantics. That split keeps
//! argument parsing from becoming a second implementation of contract validation and makes the
//! read-only `verify` path explicit in the handler trait.

use std::ffi::OsString;
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};

use clap::{Arg, Command, value_parser};

use serde::Serialize;
use serde::de::DeserializeOwned;

use crate::canonical::{DigestDomain, canonical_bytes, canonical_digest, decode_canonical};
use crate::contract::derive_contract;
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, ToolError};
use crate::io::IsolatedStaging;
use crate::model::{
    AuthorityRegistry, CanonicalInventory, CanonicalSet, CompletenessReport, EvidenceRegistry,
    HarnessCheckResult, HarnessMappings, RepositoryRelativePath, ResolvedLedger,
    RustApiTransitionReview, TargetDescriptor, TargetDigest,
};
use crate::report::{Gate, build_report, gate_exit_status, render_human_report};
use crate::transition::{ContractSnapshot, diff_targets};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum GateArgument {
    Integrity,
    Completeness,
}

impl From<GateArgument> for Gate {
    fn from(value: GateArgument) -> Self {
        match value {
            GateArgument::Integrity => Self::Integrity,
            GateArgument::Completeness => Self::Completeness,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ReportFormat {
    Human,
    Json,
}

/// Contract orchestration invoked by the thin command surface.
///
/// `verify` receives no output or retrieval adapter, which makes filesystem mutation and network
/// acquisition structurally unavailable on the normal verification path.
pub trait CliBackend {
    /// Produces the complete report for the checked-in contract without side effects.
    fn verify(&self, root: &Path) -> Result<CompletenessReport, ToolError>;

    /// Renders derived artifacts into `staging` and returns their report.
    fn render(
        &self,
        root: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError>;

    /// Assesses a candidate target and renders the proposed transition into `staging`.
    fn transition(
        &self,
        root: &Path,
        candidate: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError>;

    /// Imports a normalized evidence run into `staging` without editing active artifacts.
    fn import_evidence(
        &self,
        root: &Path,
        run: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError>;
}

#[derive(Clone, Copy, Debug, Default)]
/// Filesystem adapter for canonical checked-in and candidate contract artifacts.
///
/// The backend validates report derivation, stages reviewed transition bundles, and admits bounded
/// harness results. Source extraction and network retrieval remain outside this adapter: Dagger
/// automation supplies already-derived candidate trees before invoking the thin command surface.
pub struct ArtifactCliBackend;

#[derive(Clone, Copy, Debug, Default)]
/// Source-derived backend used by the production completeness command.
///
/// Verification reconstructs every artifact from pinned inputs before comparing checked bytes.
/// Rendering performs the same reconstruction without comparison and writes only to isolated
/// staging, keeping authored inputs and the active contract tree immutable.
pub struct ContractCliBackend;

impl CliBackend for ContractCliBackend {
    fn verify(&self, root: &Path) -> Result<CompletenessReport, ToolError> {
        derive_contract(root, true).map(|derived| derived.report)
    }

    fn render(
        &self,
        root: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError> {
        let derived = derive_contract(root, false)?;
        write_canonical(
            staging,
            "artifacts/source-items.json",
            &derived.source_items,
        )?;
        write_canonical(staging, "artifacts/inventory.json", &derived.inventory)?;
        write_canonical(staging, "artifacts/ledger.json", &derived.ledger)?;
        write_canonical(
            staging,
            "artifacts/release-compatibility.json",
            &derived.release_metadata,
        )?;
        write_report(staging, &derived.report)?;
        Ok(derived.report)
    }

    fn transition(
        &self,
        root: &Path,
        candidate: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError> {
        ArtifactCliBackend.transition(root, candidate, staging)
    }

    fn import_evidence(
        &self,
        root: &Path,
        run: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError> {
        ArtifactCliBackend.import_evidence(root, run, staging)
    }
}

impl CliBackend for ArtifactCliBackend {
    fn verify(&self, root: &Path) -> Result<CompletenessReport, ToolError> {
        load_verified_report(&contract_root(root))
    }

    fn render(
        &self,
        root: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError> {
        let contract = contract_root(root);
        let report = load_verified_report(&contract)?;
        let inventory: CanonicalInventory =
            read_canonical(&contract.join("artifacts/inventory.json"), "inventory")?;
        let ledger: ResolvedLedger =
            read_canonical(&contract.join("artifacts/ledger.json"), "ledger")?;
        write_canonical(staging, "artifacts/inventory.json", &inventory)?;
        write_canonical(staging, "artifacts/ledger.json", &ledger)?;
        write_report(staging, &report)?;
        Ok(report)
    }

    fn transition(
        &self,
        root: &Path,
        candidate: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError> {
        let current = ContractArtifacts::load(&contract_root(root), None)?;
        let candidate_root = candidate.parent().ok_or(ToolError::Decode {
            artifact: "candidate target parent",
        })?;
        let successor = ContractArtifacts::load(candidate_root, Some(candidate))?;
        let review_path = candidate_root.join("transition-reviews.json");
        let reviews = if review_path.exists() {
            read_canonical(&review_path, "transition reviews")?
        } else {
            CanonicalSet::<RustApiTransitionReview>::default()
        };
        let transition =
            match diff_targets(current.as_snapshot(), successor.as_snapshot(), &reviews) {
                Ok(transition) => transition,
                Err(diagnostics) => {
                    return Ok(build_report(
                        successor.target.contract_format_version.clone(),
                        &successor.target,
                        &successor.authorities,
                        &successor.inventory,
                        &successor.ledger,
                        diagnostics.into_inner(),
                    ));
                }
            };
        let report = build_report(
            successor.target.contract_format_version.clone(),
            &successor.target,
            &successor.authorities,
            &successor.inventory,
            &successor.ledger,
            [],
        );

        write_canonical(staging, "target.json", &successor.target)?;
        write_canonical(staging, "authorities.json", &successor.authorities)?;
        write_canonical(staging, "harness-mappings.json", &successor.harness)?;
        write_canonical(staging, "evidence/registry.json", &successor.evidence)?;
        write_canonical(staging, "artifacts/inventory.json", &successor.inventory)?;
        write_canonical(staging, "artifacts/ledger.json", &successor.ledger)?;
        let from = transition
            .from_target
            .digest()
            .as_str()
            .strip_prefix("sha256:")
            .expect("Digest always uses the sha256 prefix");
        let to_digest = TargetDigest::new(
            canonical_digest(DigestDomain::Target, &transition.to_target)
                .expect("validated TargetDescriptor must have a canonical target digest"),
        );
        let to = to_digest
            .digest()
            .as_str()
            .strip_prefix("sha256:")
            .expect("Digest always uses the sha256 prefix");
        write_canonical(
            staging,
            &format!("transitions/{from}-to-{to}.json"),
            &transition,
        )?;
        write_report(staging, &report)?;
        Ok(report)
    }

    fn import_evidence(
        &self,
        root: &Path,
        run: &Path,
        staging: &IsolatedStaging,
    ) -> Result<CompletenessReport, ToolError> {
        let contract = contract_root(root);
        let mut report = load_verified_report(&contract)?;
        let result: HarnessCheckResult = read_canonical(run, "evidence run")?;
        let mappings: HarnessMappings =
            read_canonical(&contract.join("harness-mappings.json"), "harness mappings")?;
        let Some(mapping) = mappings.checks.get(&result.check_id) else {
            add_report_diagnostic(
                &mut report,
                ContractDiagnostic::new(
                    DiagnosticCode::HarnessCheckMissing,
                    result.check_id.to_string(),
                    None,
                    "evidence run has no reviewed harness mapping",
                ),
            );
            return Ok(report);
        };
        if let Err(diagnostics) = crate::harness::admit_harness_result(mapping, &result) {
            for diagnostic in diagnostics.into_inner() {
                add_report_diagnostic(&mut report, diagnostic);
            }
            return Ok(report);
        }

        write_canonical(
            staging,
            &format!("evidence/runs/{}.json", result.check_id),
            &result,
        )?;
        Ok(report)
    }
}

struct ContractArtifacts {
    target: TargetDescriptor,
    authorities: AuthorityRegistry,
    inventory: CanonicalInventory,
    ledger: ResolvedLedger,
    evidence: EvidenceRegistry,
    harness: HarnessMappings,
}

impl ContractArtifacts {
    fn load(root: &Path, target_path: Option<&Path>) -> Result<Self, ToolError> {
        let target_path = target_path
            .map(Path::to_path_buf)
            .unwrap_or_else(|| root.join("target.json"));
        Ok(Self {
            target: read_canonical(&target_path, "target descriptor")?,
            authorities: read_canonical(&root.join("authorities.json"), "authority registry")?,
            inventory: read_canonical(&root.join("artifacts/inventory.json"), "inventory")?,
            ledger: read_canonical(&root.join("artifacts/ledger.json"), "ledger")?,
            evidence: read_canonical(&root.join("evidence/registry.json"), "evidence registry")?,
            harness: read_canonical(&root.join("harness-mappings.json"), "harness mappings")?,
        })
    }

    fn as_snapshot(&self) -> ContractSnapshot<'_> {
        ContractSnapshot {
            target: &self.target,
            authorities: &self.authorities,
            inventory: &self.inventory,
            ledger: &self.ledger,
            evidence: &self.evidence,
            harness_mappings: &self.harness,
        }
    }
}

fn contract_root(repository_root: &Path) -> PathBuf {
    repository_root.join("sdk/rust/completeness")
}

fn read_canonical<T>(path: &Path, artifact: &'static str) -> Result<T, ToolError>
where
    T: DeserializeOwned + Serialize,
{
    let bytes =
        fs::read(path).map_err(|error| ToolError::io("read contract artifact", &error, None))?;
    decode_canonical(&bytes).map_err(|_| ToolError::Decode { artifact })
}

fn write_canonical<T: Serialize>(
    staging: &IsolatedStaging,
    relative_path: &str,
    value: &T,
) -> Result<(), ToolError> {
    let path = RepositoryRelativePath::new(relative_path).map_err(|_| ToolError::Decode {
        artifact: "staged artifact path",
    })?;
    let bytes = canonical_bytes(value).map_err(|_| ToolError::Decode {
        artifact: "staged artifact",
    })?;
    staging.write(&path, &bytes)
}

fn write_report(staging: &IsolatedStaging, report: &CompletenessReport) -> Result<(), ToolError> {
    write_canonical(staging, "artifacts/report.json", report)?;
    staging.write(
        &RepositoryRelativePath::new("artifacts/report.md")
            .expect("static report path is repository-relative"),
        render_human_report(report).as_bytes(),
    )
}

fn load_verified_report(contract: &Path) -> Result<CompletenessReport, ToolError> {
    let target: TargetDescriptor =
        read_canonical(&contract.join("target.json"), "target descriptor")?;
    let authorities: AuthorityRegistry =
        read_canonical(&contract.join("authorities.json"), "authority registry")?;
    let inventory: CanonicalInventory =
        read_canonical(&contract.join("artifacts/inventory.json"), "inventory")?;
    let ledger: ResolvedLedger = read_canonical(&contract.join("artifacts/ledger.json"), "ledger")?;
    let checked: CompletenessReport = read_canonical(
        &contract.join("artifacts/report.json"),
        "completeness report",
    )?;
    let mut expected = build_report(
        target.contract_format_version.clone(),
        &target,
        &authorities,
        &inventory,
        &ledger,
        checked.integrity_errors.clone(),
    );
    for diagnostic in report_mismatches(&checked, &expected) {
        add_report_diagnostic(&mut expected, diagnostic);
    }

    let human = fs::read(contract.join("artifacts/report.md"))
        .map_err(|error| ToolError::io("read human report", &error, None))?;
    if human != render_human_report(&checked).as_bytes() {
        add_report_diagnostic(
            &mut expected,
            ContractDiagnostic::new(
                DiagnosticCode::ReportErrorSetMismatch,
                "artifacts/report.md",
                None,
                "human report is not the pure projection of report.json",
            ),
        );
    }
    Ok(expected)
}

fn report_mismatches(
    checked: &CompletenessReport,
    expected: &CompletenessReport,
) -> Vec<ContractDiagnostic> {
    let mut diagnostics = Vec::new();
    let mut add = |code, detail| {
        diagnostics.push(ContractDiagnostic::new(
            code,
            "artifacts/report.json",
            None,
            detail,
        ));
    };
    if checked.contract_format_version != expected.contract_format_version
        || checked.target_descriptor != expected.target_descriptor
    {
        add(
            DiagnosticCode::ReportTargetMismatch,
            "report target projection differs from target.json",
        );
    }
    if checked.inventory_digest != expected.inventory_digest
        || checked.ledger_digest != expected.ledger_digest
    {
        add(
            DiagnosticCode::ReportDigestMismatch,
            "report artifact digests do not match current artifacts",
        );
    }
    if checked.integrity_verdict != expected.integrity_verdict
        || checked.completeness_verdict != expected.completeness_verdict
    {
        add(
            DiagnosticCode::ReportVerdictInvalid,
            "report verdicts do not match diagnostics and blockers",
        );
    }
    if checked.counts_by_authority != expected.counts_by_authority
        || checked.counts_by_kind != expected.counts_by_kind
        || checked.counts_by_status != expected.counts_by_status
        || checked.counts_by_owner != expected.counts_by_owner
    {
        add(
            DiagnosticCode::ReportCountMismatch,
            "report counts do not match inventory and ledger rows",
        );
    }
    if checked.blocking_capabilities != expected.blocking_capabilities {
        add(
            DiagnosticCode::ReportBlockerSetMismatch,
            "report blocker set does not match ledger statuses",
        );
    }
    if checked.complete_exceptions != expected.complete_exceptions {
        add(
            DiagnosticCode::ReportExceptionSetMismatch,
            "report exception set does not match reviewed complete statuses",
        );
    }
    diagnostics
}

fn add_report_diagnostic(report: &mut CompletenessReport, diagnostic: ContractDiagnostic) {
    report.integrity_errors.push(diagnostic);
    report.integrity_errors.sort_unstable();
    report.integrity_verdict = false;
    report.completeness_verdict = false;
}

/// Parses and executes one CLI invocation using separately supplied output streams.
///
/// Clap invocation failures and operational tool failures return status 2. A complete report uses
/// status 0 or 1 according to the selected gate, and always sends its representation to stdout
/// while human diagnostics go to stderr.
pub fn run_with_backend<I, T>(
    arguments: I,
    backend: &impl CliBackend,
    stdout: &mut impl Write,
    stderr: &mut impl Write,
) -> u8
where
    I: IntoIterator<Item = T>,
    T: Into<OsString> + Clone,
{
    let matches = match command().try_get_matches_from(arguments) {
        Ok(matches) => matches,
        Err(error) => {
            let _ = write!(stderr, "{error}");
            return ToolError::EXIT_STATUS;
        }
    };

    let result = match matches.subcommand() {
        Some(("verify", arguments)) => {
            let root = required_path(arguments, "root");
            let gate = match required_string(arguments, "gate") {
                "integrity" => GateArgument::Integrity,
                "completeness" => GateArgument::Completeness,
                _ => unreachable!("clap constrains gate values"),
            };
            let format = match required_string(arguments, "format") {
                "human" => ReportFormat::Human,
                "json" => ReportFormat::Json,
                _ => unreachable!("clap constrains format values"),
            };
            backend
                .verify(root)
                .map(|report| (report, gate.into(), format))
        }
        Some(("render", arguments)) => {
            let root = required_path(arguments, "root");
            let output = required_path(arguments, "output");
            with_staging(root, output, |staging| {
                backend
                    .render(root, staging)
                    .map(|report| (report, Gate::Integrity, ReportFormat::Json))
            })
        }
        Some(("transition", arguments)) => {
            let root = required_path(arguments, "root");
            let candidate = required_path(arguments, "candidate");
            let output = required_path(arguments, "output");
            with_staging(root, output, |staging| {
                backend
                    .transition(root, candidate, staging)
                    .map(|report| (report, Gate::Integrity, ReportFormat::Json))
            })
        }
        Some(("import-evidence", arguments)) => {
            let root = required_path(arguments, "root");
            let run = required_path(arguments, "run");
            let output = required_path(arguments, "output");
            with_staging(root, output, |staging| {
                backend
                    .import_evidence(root, run, staging)
                    .map(|report| (report, Gate::Integrity, ReportFormat::Json))
            })
        }
        _ => unreachable!("clap requires a known subcommand"),
    };

    match result {
        Ok((report, gate, format)) => {
            for diagnostic in &report.integrity_errors {
                let locator = diagnostic
                    .locator
                    .as_ref()
                    .map(|locator| format!(" {locator}"))
                    .unwrap_or_default();
                let _ = writeln!(
                    stderr,
                    "{} {}{}: {}",
                    diagnostic.code, diagnostic.subject, locator, diagnostic.detail
                );
            }
            let output = match format {
                ReportFormat::Human => render_human_report(&report).into_bytes(),
                ReportFormat::Json => match canonical_bytes(&report) {
                    Ok(bytes) => bytes,
                    Err(_) => {
                        let _ = writeln!(stderr, "could not encode completeness report");
                        return ToolError::EXIT_STATUS;
                    }
                },
            };
            if stdout.write_all(&output).is_err() {
                let _ = writeln!(stderr, "could not write completeness report");
                return ToolError::EXIT_STATUS;
            }
            gate_exit_status(&report, gate)
        }
        Err(error) => {
            let _ = writeln!(stderr, "{error}");
            ToolError::EXIT_STATUS
        }
    }
}

fn command() -> Command {
    let root = || {
        Arg::new("root")
            .long("root")
            .required(true)
            .value_parser(value_parser!(PathBuf))
    };
    let output = || {
        Arg::new("output")
            .long("output")
            .required(true)
            .value_parser(value_parser!(PathBuf))
    };
    Command::new("dagger-sdk-completeness")
        .about("Verify and stage the Dagger Rust SDK completeness contract")
        .subcommand_required(true)
        .subcommand(
            Command::new("verify")
                .about("Verify checked-in contract artifacts without writes or network access")
                .arg(root())
                .arg(
                    Arg::new("gate")
                        .long("gate")
                        .required(true)
                        .value_parser(["integrity", "completeness"]),
                )
                .arg(
                    Arg::new("format")
                        .long("format")
                        .required(true)
                        .value_parser(["human", "json"]),
                ),
        )
        .subcommand(
            Command::new("render")
                .about("Render derived artifacts into an empty staging directory")
                .arg(root())
                .arg(output()),
        )
        .subcommand(
            Command::new("transition")
                .about("Assess an immutable candidate target into staging")
                .arg(root())
                .arg(
                    Arg::new("candidate")
                        .long("candidate")
                        .required(true)
                        .value_parser(value_parser!(PathBuf)),
                )
                .arg(output()),
        )
        .subcommand(
            Command::new("import-evidence")
                .about("Validate a normalized evidence run into staging")
                .arg(root())
                .arg(
                    Arg::new("run")
                        .long("run")
                        .required(true)
                        .value_parser(value_parser!(PathBuf)),
                )
                .arg(output()),
        )
}

fn required_path<'a>(arguments: &'a clap::ArgMatches, name: &str) -> &'a Path {
    arguments
        .get_one::<PathBuf>(name)
        .expect("clap requires every command path")
        .as_path()
}

fn required_string<'a>(arguments: &'a clap::ArgMatches, name: &str) -> &'a str {
    arguments
        .get_one::<String>(name)
        .expect("clap requires every constrained command value")
}

fn with_staging<T>(
    repository_root: &Path,
    output: &Path,
    operation: impl FnOnce(&IsolatedStaging) -> Result<T, ToolError>,
) -> Result<T, ToolError> {
    refuse_active_contract_output(repository_root, output)?;
    let staging = IsolatedStaging::prepare(output.to_path_buf())?;
    operation(&staging)
}

fn refuse_active_contract_output(repository_root: &Path, output: &Path) -> Result<(), ToolError> {
    let Ok(repository_root) = fs::canonicalize(repository_root) else {
        return Ok(());
    };
    let active = repository_root.join("sdk/rust/completeness");
    let Ok(active) = fs::canonicalize(active) else {
        return Ok(());
    };
    let absolute_output = if output.is_absolute() {
        output.to_path_buf()
    } else {
        std::env::current_dir()
            .map_err(|error| ToolError::io("resolve staging output", &error, None))?
            .join(output)
    };
    if absolute_output.starts_with(&active) {
        return Err(active_output_error());
    }
    let resolved_output = if output.exists() {
        fs::canonicalize(output)
    } else {
        output.parent().map_or_else(
            || Err(std::io::Error::from(std::io::ErrorKind::InvalidInput)),
            |parent| {
                fs::canonicalize(parent).map(|parent| {
                    parent.join(
                        output
                            .file_name()
                            .expect("non-existing output with a parent has a file name"),
                    )
                })
            },
        )
    };
    if resolved_output.is_ok_and(|output| output.starts_with(&active)) {
        return Err(active_output_error());
    }
    Ok(())
}

fn active_output_error() -> ToolError {
    let error = std::io::Error::new(
        std::io::ErrorKind::PermissionDenied,
        "staging output is inside the active contract tree",
    );
    ToolError::io("refuse active contract output", &error, None)
}
