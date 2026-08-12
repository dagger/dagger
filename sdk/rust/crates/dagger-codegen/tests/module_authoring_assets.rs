//! Engine-free descriptor, projection, rendering, and regeneration verification.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::PathBuf;
use std::process::Command;

use dagger_codegen::module::{
    CfgEnvironment, FormatVersion, GeneratedAssetPath, GeneratedTypeRegistry, ModuleCompilation,
    ModuleCompilationRequest, ModuleCompiler, ModulePackage, ModuleRenderRequest, ModuleRenderer,
    ModuleSourcePath, ModuleSourceSnapshot, ModuleTarget, PackageName, ProjectionCompiler,
    RegenerationPlanner, Sha256Digest, SourceDocument, TargetValue, WireName, canonical_bytes,
    manifest_digest, source_snapshot_digest,
};
use proptest::prelude::*;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn property_11_descriptor_identity_canonical_change_sensitive(
        seed in any::<u16>(),
        reverse in any::<bool>(),
        mutation in 0_u8..5,
    ) {
        let left_source = source_snapshot(seed, false, false);
        let right_source = source_snapshot(seed, reverse, false);
        let target = target(digest(format!("schema-{seed}")));
        let generator = digest(format!("generator-{seed}"));
        let left = compile(&left_source, &target, &generator, BTreeSet::new()).unwrap();
        let right = compile(&right_source, &target, &generator, BTreeSet::new()).unwrap();

        prop_assert_eq!(canonical_bytes(&left.descriptor).unwrap(), canonical_bytes(&right.descriptor).unwrap());
        prop_assert_eq!(&left.descriptor.digest, &right.descriptor.digest);

        let mut changed_source = left_source.clone();
        let mut changed_target = target.clone();
        let mut changed_generator = generator.clone();
        match mutation {
            0 => changed_source = source_snapshot(seed, false, true),
            1 => {
                changed_source.cfg.features.insert("changed".to_owned());
                changed_source.digest = source_snapshot_digest(&changed_source).unwrap();
            }
            2 => changed_target.visible_schema_digest = digest(format!("schema-changed-{seed}")),
            3 => changed_generator = digest(format!("generator-changed-{seed}")),
            _ => changed_target.engine_version = value(format!("engine-changed-{seed}")),
        }
        let changed = compile(
            &changed_source,
            &changed_target,
            &changed_generator,
            BTreeSet::new(),
        ).unwrap();
        prop_assert_ne!(&left.descriptor.digest, &changed.descriptor.digest);
        match mutation {
            0 => prop_assert_ne!(&left.descriptor.provenance.source_files, &changed.descriptor.provenance.source_files),
            1 => prop_assert_ne!(&left.descriptor.provenance.cfg, &changed.descriptor.provenance.cfg),
            2 => prop_assert_ne!(&left.descriptor.provenance.visible_schema_digest, &changed.descriptor.provenance.visible_schema_digest),
            3 => prop_assert_ne!(&left.descriptor.provenance.generator_digest, &changed.descriptor.provenance.generator_digest),
            _ => prop_assert_ne!(&left.descriptor.target, &changed.descriptor.target),
        }
    }

    #[test]
    fn property_12_registration_introspection_equivalent_projections(seed in any::<u16>()) {
        let source = source_snapshot(seed, false, false);
        let target = target(digest(format!("schema-{seed}")));
        let compiled = compile(&source, &target, &digest("generator"), BTreeSet::new()).unwrap();

        prop_assert_eq!(&compiled.registration.types, &compiled.introspection.types);
        prop_assert_eq!(&compiled.registration.descriptor_digest, &compiled.descriptor.digest);
        let query = compiled.registration.types.get(&WireName::new("Query").unwrap()).unwrap();
        prop_assert_eq!(query.functions.len(), 1);
        prop_assert!(query.functions[0].constructor);
        prop_assert_eq!(compiled.registration.types.len(), compiled.descriptor.types.len() + 1);
        let context_path = GeneratedAssetPath::new("src/dagger_generated/module_context.rs").unwrap();
        let context = std::str::from_utf8(&compiled.assets.files[&context_path]).unwrap();
        prop_assert!(context.contains("root: renamed_sdk::Query"));
        prop_assert!(context.contains("generated_query_root"));
        prop_assert!(context.contains("impl ::core::ops::Deref for ModuleQuery"));
        prop_assert!(context.contains("pub fn current_function_call"));
        prop_assert!(context.contains("pub fn current_module"));
        prop_assert!(context.contains("pub fn current_node"));
        for forbidden in ["connect()", "static mut", "Serialize for ModuleContext"] {
            prop_assert!(!context.contains(forbidden));
        }

        let root_name = compiled
            .descriptor
            .types
            .iter()
            .find(|ty| ty.rust_symbol == compiled.descriptor.root)
            .unwrap()
            .wire_name
            .clone();
        let collision = ProjectionCompiler::project(
            &compiled.descriptor,
            &BTreeSet::from([root_name]),
        );
        prop_assert!(collision.is_err());
    }

    #[test]
    fn property_13_dispatch_registry_total_closed_mapping(seed in any::<u16>()) {
        let source = source_snapshot(seed, false, false);
        let target = target(digest(format!("schema-{seed}")));
        let compiled = compile(&source, &target, &digest("generator"), BTreeSet::new()).unwrap();
        let path = GeneratedAssetPath::new("src/dagger_generated/module_dispatch.rs").unwrap();
        let dispatch = String::from_utf8(compiled.assets.files[&path].clone()).unwrap();

        for function in &compiled.descriptor.functions {
            let arm = format!("{:?} => {{", function.wire_name.as_str());
            prop_assert_eq!(dispatch.matches(&arm).count(), 1);
            let fingerprint = function.fingerprint.as_u128().unwrap();
            let bridge = format!("__dagger_bridge_{}_{}", function.compiled.rust_name, fingerprint);
            prop_assert_eq!(dispatch.matches(&bridge).count(), 1);
        }
        prop_assert!(dispatch.contains("InvocationError::UnknownParent"));
        prop_assert!(dispatch.contains("InvocationError::UnknownFunction"));
        for forbidden in ["downcast", "reflection", "fallback", "inventory::"] {
            prop_assert!(!dispatch.contains(forbidden));
        }

        let duplicate_source = source_snapshot(seed, false, false).with_duplicate_function();
        let duplicate = compile(&duplicate_source, &target, &digest("generator"), BTreeSet::new());
        prop_assert!(duplicate.is_err());
    }

    #[test]
    fn property_27_compile_fixtures_fence_public_authoring_contract(
        seed in any::<u16>(),
        category in 0_u8..7,
    ) {
        let source = compile_contract_source(seed, category);
        let target = target(digest(format!("compile-contract-schema-{seed}")));
        let generator = digest(format!("compile-contract-generator-{seed}"));
        let first = compile(&source, &target, &generator, BTreeSet::new());
        let second = compile(&source, &target, &generator, BTreeSet::new());
        let expected_pass = category == 0;

        prop_assert_eq!(first.is_ok(), expected_pass);
        prop_assert_eq!(second.is_ok(), expected_pass);
        match (first, second) {
            (Ok(left), Ok(right)) => {
                prop_assert_eq!(left.descriptor.digest, right.descriptor.digest);
                prop_assert_eq!(left.assets, right.assets);
            }
            (Err(left), Err(right)) => {
                prop_assert_eq!(&left, &right);
                if category >= 2 {
                    prop_assert!(left.diagnostics().iter().any(|diagnostic| diagnostic.source_coordinate().is_some()));
                }
            }
            _ => prop_assert!(false, "classification changed between identical in-memory compiles"),
        }

        // Every in-memory family is paired with the one bounded trybuild batch rather
        // than spawning Cargo for randomized cases.
        let real_compile_fixture = match category {
            0 => "pass/foundations.rs",
            1 => "fail/duplicate_metadata.rs",
            2 => "fail/private_object.rs",
            3 => "fail/state_signature_mismatch.rs",
            4 => "fail/fingerprint_mismatch.rs",
            5 => "fail/payload_enum.rs",
            _ => "fail/duplicate_metadata.rs",
        };
        let fixture_root = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .parent()
            .expect("codegen and SDK crates share a parent")
            .join("dagger-sdk/tests/fixtures/module_authoring")
            .join(real_compile_fixture);
        prop_assert!(fixture_root.is_file());
    }

    #[test]
    fn property_25_regeneration_scoped_deterministic_convergent(
        seed in any::<u16>(),
        missing_index in any::<usize>(),
        change in 0_u8..4,
    ) {
        let source = source_snapshot(seed, false, false);
        let baseline_target = target(digest(format!("schema-{seed}")));
        let baseline = compile(&source, &baseline_target, &digest("generator"), BTreeSet::new()).unwrap();
        let baseline_observed = observed(&baseline);
        let no_op = RegenerationPlanner::plan(
            Some(&baseline.assets.manifest),
            &baseline.assets.manifest,
            &baseline_observed,
        ).unwrap();
        prop_assert!(no_op.selected.is_empty());
        prop_assert!(no_op.removed.is_empty());
        prop_assert!(no_op.changed_domains.is_empty());

        let mut inconsistent = baseline.assets.manifest.clone();
        let record = inconsistent.assets.values_mut().next().unwrap();
        record.path = GeneratedAssetPath::new("src/dagger_generated/wrong-owner.rs").unwrap();
        inconsistent.digest = manifest_digest(&inconsistent).unwrap();
        prop_assert!(RegenerationPlanner::plan(
            Some(&inconsistent),
            &baseline.assets.manifest,
            &baseline_observed,
        ).is_err());

        let mut missing = baseline_observed.clone();
        let missing_path = baseline.assets.manifest.assets.keys().nth(
            missing_index % baseline.assets.manifest.assets.len()
        ).unwrap().clone();
        missing.remove(&missing_path);
        let repair = RegenerationPlanner::plan(
            Some(&baseline.assets.manifest),
            &baseline.assets.manifest,
            &missing,
        ).unwrap();
        prop_assert_eq!(repair.selected, BTreeSet::from([missing_path]));

        let obsolete_path = baseline.assets.manifest.assets.keys().next().unwrap().clone();
        let mut pruned = baseline.assets.manifest.clone();
        pruned.assets.remove(&obsolete_path);
        pruned.digest = manifest_digest(&pruned).unwrap();
        let unknown_path = GeneratedAssetPath::new("user-owned/notes.txt").unwrap();
        let mut observed_with_unknown = baseline_observed.clone();
        observed_with_unknown.insert(unknown_path.clone(), digest("unknown-user-bytes"));
        let removal = RegenerationPlanner::plan(
            Some(&baseline.assets.manifest),
            &pruned,
            &observed_with_unknown,
        ).unwrap();
        prop_assert_eq!(removal.removed, BTreeSet::from([obsolete_path]));
        prop_assert!(!removal.selected.contains(&unknown_path));

        let mut changed_source = source.clone();
        let mut changed_target = baseline_target.clone();
        let changed_generator;
        match change {
            0 => {
                changed_source = source_snapshot(seed, false, true);
                changed_generator = digest("generator");
            }
            1 => {
                changed_target.visible_schema_digest = digest(format!("schema-next-{seed}"));
                changed_generator = digest("generator");
            }
            2 => {
                changed_target.engine_version = value(format!("engine-next-{seed}"));
                changed_generator = digest("generator");
            }
            _ => changed_generator = digest(format!("generator-next-{seed}")),
        }
        let changed = compile(
            &changed_source,
            &changed_target,
            &changed_generator,
            BTreeSet::new(),
        ).unwrap();
        let plan = RegenerationPlanner::plan(
            Some(&baseline.assets.manifest),
            &changed.assets.manifest,
            &baseline_observed,
        ).unwrap();
        prop_assert!(!plan.selected.is_empty());
        prop_assert!(plan.selected.iter().all(|path| changed.assets.manifest.assets.contains_key(path)));
        if matches!(change, 2 | 3) {
            prop_assert_eq!(plan.selected.len(), changed.assets.manifest.assets.len());
        }
        let converged = RegenerationPlanner::plan(
            Some(&changed.assets.manifest),
            &changed.assets.manifest,
            &observed(&changed),
        ).unwrap();
        prop_assert_eq!(converged, Default::default());
    }

    #[test]
    fn property_24_pure_generation_rejection_returns_no_partial_compilation(
        seed in any::<u16>(),
        failure in 0_u8..4,
    ) {
        let source = source_snapshot(seed, false, false);
        let target = target(digest(format!("schema-{seed}")));
        let generator = digest(format!("generator-{seed}"));
        let baseline = compile(&source, &target, &generator, BTreeSet::new()).unwrap();

        let rejected = match failure {
            0 => {
                let stale = GeneratedTypeRegistry::empty(digest(format!("stale-schema-{seed}")));
                ModuleCompiler::compile(ModuleCompilationRequest {
                    target: &target,
                    source: &source,
                    generated_types: &stale,
                    visible_type_names: &BTreeSet::new(),
                    generator_digest: &generator,
                    sdk_dependency_alias: "renamed_sdk",
                    checked_bindings: &BTreeMap::new(),
                }).map(|compilation| compilation.assets)
            }
            1 => compile(
                &source.clone().with_duplicate_function(),
                &target,
                &generator,
                BTreeSet::new(),
            ).map(|compilation| compilation.assets),
            2 => {
                let root_name = baseline
                    .descriptor
                    .types
                    .iter()
                    .find(|ty| ty.rust_symbol == baseline.descriptor.root)
                    .unwrap()
                    .wire_name
                    .clone();
                compile(
                    &source,
                    &target,
                    &generator,
                    BTreeSet::from([root_name]),
                ).map(|compilation| compilation.assets)
            }
            _ => {
                let mut introspection = baseline.introspection.clone();
                introspection.descriptor_digest = digest(format!("wrong-descriptor-{seed}"));
                ModuleRenderer::render(ModuleRenderRequest {
                    descriptor: &baseline.descriptor,
                    registration: &baseline.registration,
                    introspection: &introspection,
                    sdk_dependency_alias: "renamed_sdk",
                    checked_bindings: &BTreeMap::new(),
                })
            }
        };
        prop_assert!(rejected.is_err());
    }
}

#[test]
fn representative_generated_module_executes_directly_offline() {
    let source = source_snapshot(7, false, false);
    let target = target(digest("checked-schema"));
    let compiled = compile(
        &source,
        &target,
        &digest("checked-generator"),
        BTreeSet::new(),
    )
    .expect("representative module must compile purely");
    for (path, bytes) in &compiled.assets.files {
        if path.as_str().ends_with(".rs") {
            syn::parse_file(std::str::from_utf8(bytes).expect("generated Rust is UTF-8"))
                .unwrap_or_else(|error| panic!("{} must parse: {error}", path.as_str()));
        }
    }

    let temporary = tempfile::tempdir().expect("fixture root must be available");
    let root = temporary.path();
    let manifest_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let sdk = manifest_dir
        .parent()
        .expect("codegen and SDK crates share a parent")
        .join("dagger-sdk");
    fs::create_dir_all(root.join("src")).expect("fixture source root must be created");
    fs::write(
        root.join("Cargo.toml"),
        format!(
            "[package]\nname = \"fixture\"\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\nrenamed_sdk = {{ package = \"dagger-sdk\", path = {:?} }}\ntokio = {{ version = \"1\", features = [\"rt\"] }}\nasync-trait = \"0.1\"\n",
            sdk
        ),
    )
    .expect("fixture manifest must write");
    for document in source.documents.values() {
        let destination = root.join(document.path.as_str());
        fs::create_dir_all(destination.parent().expect("document has a parent"))
            .expect("document parent must be created");
        let contents = if document.path.as_str() == "src/lib.rs" {
            format!("{}\npub mod dagger_generated;\n", document.contents)
        } else {
            document.contents.clone()
        };
        fs::write(destination, contents).expect("fixture source must write");
    }
    for (path, bytes) in &compiled.assets.files {
        if !path.as_str().ends_with(".rs") {
            continue;
        }
        let destination = root.join(path.as_str());
        fs::create_dir_all(destination.parent().expect("asset has a parent"))
            .expect("asset parent must be created");
        fs::write(destination, bytes).expect("generated source must write");
    }
    fs::create_dir_all(root.join("tests")).expect("fixture test root must be created");
    fs::write(
        root.join("tests/direct_harness.rs"),
        direct_harness_source(),
    )
    .expect("direct harness must write");
    let target_dir = temporary.path().join("target");
    let output = Command::new(env!("CARGO"))
        .args(["test", "--all-targets", "--offline", "--quiet"])
        .env("CARGO_TARGET_DIR", &target_dir)
        .current_dir(root)
        .output()
        .expect("offline fixture cargo must start");
    assert!(
        output.status.success(),
        "generated fixture failed:\n{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

fn direct_harness_source() -> &'static str {
    r#"
use std::sync::{Arc, Mutex};

use async_trait::async_trait;
use fixture::dagger_generated::GeneratedDispatchRegistry;
use renamed_sdk::{
    Client, ClientConfig, CurrentCall, EngineConnection, EngineConnectionError, ModuleCancellation,
    RawRequest, RawResponse, ResponseData,
};
use renamed_sdk::__private::{
    CallEnvelope, CallIdentity, CallOutcome, CallSelector, ModuleBoxFuture, ModuleContextBase,
    ModuleFunctionName, ModuleJson, ModuleWireName, NamedModuleArgument, RegistrationError,
    RegistrationSink,
    RegistrationView, ResultPublishError, ResultSink, handle_call, serde_json,
};

#[derive(Clone)]
struct NoEngine(Arc<Mutex<Vec<String>>>);

#[async_trait]
impl EngineConnection for NoEngine {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.0.lock().expect("event lock").push(request.query().to_owned());
        Ok(RawResponse::new(ResponseData::Null))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> { Ok(()) }

    fn abort(&self) {}
}

struct RegistrationRecorder(Arc<Mutex<Vec<String>>>);

impl RegistrationSink for RegistrationRecorder {
    fn serve<'a>(
        &'a self,
        registration: &'a RegistrationView,
    ) -> ModuleBoxFuture<'a, Result<(), RegistrationError>> {
        Box::pin(async move {
            self.0
                .lock()
                .expect("registration lock")
                .push(registration.descriptor_digest.to_owned());
            Ok(())
        })
    }
}

struct ResultRecorder(Arc<Mutex<Vec<CallOutcome>>>);

impl ResultSink for ResultRecorder {
    fn publish<'a>(
        &'a self,
        outcome: CallOutcome,
    ) -> ModuleBoxFuture<'a, Result<(), ResultPublishError>> {
        Box::pin(async move {
            self.0.lock().expect("result lock").push(outcome);
            Ok(())
        })
    }
}

fn selector(function: &str) -> CallSelector {
    CallSelector::Invocation {
        parent_wire_name: ModuleWireName::new("FixtureRoot").expect("parent name"),
        function_wire_name: ModuleFunctionName::new(function).expect("function name"),
    }
}

fn envelope(
    call_id: &str,
    function: &str,
    parent: Option<serde_json::Value>,
    arguments: &[(&str, serde_json::Value)],
) -> CallEnvelope {
    let selector = selector(function);
    CallEnvelope::new(
        CallIdentity::new(call_id, selector).expect("call identity"),
        parent.map(ModuleJson::new),
        arguments
            .iter()
            .map(|(name, value)| NamedModuleArgument {
                name: ModuleWireName::new(*name).expect("argument name"),
                value: ModuleJson::new(value.clone()),
            })
            .collect(),
    )
}

fn context(client: &Client, envelope: &CallEnvelope) -> ModuleContextBase {
    ModuleContextBase::new(
        client.query_builder(),
        ModuleCancellation::default(),
        Default::default(),
        CurrentCall::new(
            envelope.identity.call_id(),
            envelope.identity.selector().clone(),
        )
        .expect("current call"),
    )
}

#[test]
fn generated_registry_runs_through_the_production_wrapper_without_an_engine() {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("runtime");
    runtime.block_on(async {
        let engine_events = Arc::new(Mutex::new(Vec::new()));
        let client = renamed_sdk::connect_with(
            ClientConfig::builder()
                .connection(Box::new(NoEngine(Arc::clone(&engine_events))))
                .build()
                .expect("client config"),
        )
        .await
        .expect("injected client");
        let registrations = Arc::new(Mutex::new(Vec::new()));
        let outcomes = Arc::new(Mutex::new(Vec::new()));
        let registration_sink = RegistrationRecorder(Arc::clone(&registrations));
        let result_sink = ResultRecorder(Arc::clone(&outcomes));

        let registration = CallEnvelope::new(
            CallIdentity::new("registration", CallSelector::Registration).expect("identity"),
            None,
            Vec::new(),
        );
        handle_call(
            &GeneratedDispatchRegistry,
            registration.clone(),
            context(&client, &registration),
            &registration_sink,
            &result_sink,
        )
        .await
        .expect("registration");

        let constructor = envelope(
            "constructor",
            "new",
            None,
            &[("child", serde_json::json!({"value": "hello"}))],
        );
        handle_call(
            &GeneratedDispatchRegistry,
            constructor.clone(),
            context(&client, &constructor),
            &registration_sink,
            &result_sink,
        )
        .await
        .expect("constructor");
        let parent = match outcomes.lock().expect("result lock").pop().expect("state") {
            CallOutcome::Value(value) => value.into_json(),
            outcome => panic!("unexpected constructor outcome: {outcome:?}"),
        };

        for (call_id, arguments, expected) in [
            ("default", Vec::new(), "hello-world-7-stable"),
            (
                "explicit",
                vec![("name", serde_json::json!("friend"))],
                "hello-friend-7-stable",
            ),
        ] {
            let invocation = envelope(call_id, "greet", Some(parent.clone()), &arguments);
            handle_call(
                &GeneratedDispatchRegistry,
                invocation.clone(),
                context(&client, &invocation),
                &registration_sink,
                &result_sink,
            )
            .await
            .expect("invocation");
            let value = match outcomes.lock().expect("result lock").pop().expect("value") {
                CallOutcome::Value(value) => value.into_json(),
                outcome => panic!("unexpected invocation outcome: {outcome:?}"),
            };
            assert_eq!(value, serde_json::json!(expected));
        }

        let unknown = envelope("unknown", "absent", Some(parent), &[]);
        assert!(
            handle_call(
                &GeneratedDispatchRegistry,
                unknown.clone(),
                context(&client, &unknown),
                &registration_sink,
                &result_sink,
            )
            .await
            .is_err()
        );
        assert_eq!(registrations.lock().expect("registration lock").len(), 1);
        assert!(outcomes.lock().expect("result lock").is_empty());
        assert!(engine_events.lock().expect("event lock").is_empty());
        client.close().await.expect("fixture client close");
    });
}
"#
}

fn compile(
    source: &ModuleSourceSnapshot,
    target: &ModuleTarget,
    generator: &Sha256Digest,
    visible_type_names: BTreeSet<WireName>,
) -> Result<ModuleCompilation, dagger_codegen::module::ModuleDiagnosticSet> {
    let generated = GeneratedTypeRegistry::empty(target.visible_schema_digest.clone());
    ModuleCompiler::compile(ModuleCompilationRequest {
        target,
        source,
        generated_types: &generated,
        visible_type_names: &visible_type_names,
        generator_digest: generator,
        sdk_dependency_alias: "renamed_sdk",
        checked_bindings: &BTreeMap::new(),
    })
}

fn source_snapshot(seed: u16, reverse: bool, changed: bool) -> ModuleSourceSnapshot {
    let suffix = if changed { "changed" } else { "stable" };
    let root = format!(
        r#"
mod child;

#[renamed_sdk::enum_type]
pub(crate) enum Mood {{
    /// Ready for execution.
    Ready,
    /// Retained only for compatibility.
    #[deprecated(note = "use Ready")]
    Waiting,
}}

#[renamed_sdk::object(root, rename = "FixtureRoot")]
pub(crate) struct Root {{
    #[dagger(field)]
    child: child::Child,
}}

#[renamed_sdk::methods]
impl Root {{
    #[dagger(constructor)]
    pub(crate) fn new(child: child::Child) -> Root {{ Root {{ child }} }}

    #[dagger(function, cache = "never", role = "check")]
    pub(crate) fn greet(&self, #[dagger(default = "world")] name: String) -> String {{
        format!("{{}}-{{name}}-{seed}-{suffix}", self.child.value)
    }}
}}
"#
    );
    let child = r#"
#[renamed_sdk::object(rename = "FixtureChild")]
pub(crate) struct Child {
    #[dagger(field)]
    pub(crate) value: String,
}
"#
    .to_owned();
    let mut entries = vec![("src/lib.rs", root), ("src/child.rs", child)];
    if reverse {
        entries.reverse();
    }
    snapshot(entries)
}

fn compile_contract_source(seed: u16, category: u8) -> ModuleSourceSnapshot {
    let mut snapshot = source_snapshot(seed, false, false);
    let root_path = ModuleSourcePath::new("src/lib.rs").expect("fixture root path is valid");
    let document = snapshot
        .documents
        .get_mut(&root_path)
        .expect("fixture root exists");
    match category {
        0 => {}
        1 => {
            document.contents.push_str(
                r#"
#[renamed_sdk::methods]
impl Root {
    #[dagger(function, rename = "greet")]
    pub(crate) fn duplicate(&self) -> String { String::new() }
}
"#,
            );
        }
        2 => {
            document.contents = document
                .contents
                .replace("pub(crate) struct Root", "struct Root")
        }
        3 => {
            document.contents = document
                .contents
                .replace("child: child::Child", "#[dagger(state)]\n    child: usize")
        }
        4 => document.contents = document.contents.replace("name: String", "name: u64"),
        5 => {
            document.contents = document
                .contents
                .replace("#[dagger(default = \"world\")]", "#[dagger(default = 7)]")
        }
        _ => {
            document.contents.push_str(
                r#"
#[renamed_sdk::object(root)]
pub(crate) struct OtherRoot;
"#,
            );
        }
    }
    document.digest = Sha256Digest::hash_bytes(document.contents.as_bytes());
    snapshot.digest = source_snapshot_digest(&snapshot).expect("fixture snapshot must hash");
    snapshot
}

fn snapshot(entries: Vec<(&str, String)>) -> ModuleSourceSnapshot {
    let documents = entries
        .into_iter()
        .map(|(path, contents)| {
            let path = ModuleSourcePath::new(path).expect("fixture path is valid");
            (path.clone(), SourceDocument::new(path, contents))
        })
        .collect();
    let mut snapshot = ModuleSourceSnapshot {
        format_version: FormatVersion::current(),
        package: ModulePackage {
            name: PackageName::new("fixture").expect("fixture package is valid"),
            crate_root: ModuleSourcePath::new("src/lib.rs").expect("fixture root is valid"),
            edition: value("2024"),
        },
        cfg: CfgEnvironment {
            values: BTreeMap::from([("unix".to_owned(), BTreeSet::new())]),
            features: BTreeSet::new(),
        },
        documents,
        digest: digest("pending"),
    };
    snapshot.digest = source_snapshot_digest(&snapshot).expect("fixture snapshot must hash");
    snapshot
}

trait SnapshotMutation {
    fn with_duplicate_function(self) -> Self;
}

impl SnapshotMutation for ModuleSourceSnapshot {
    fn with_duplicate_function(mut self) -> Self {
        let path = ModuleSourcePath::new("src/lib.rs").expect("fixture root is valid");
        let document = self.documents.get_mut(&path).expect("fixture root exists");
        document.contents.push_str(
            r#"
#[renamed_sdk::methods]
impl Root {
    #[dagger(function, rename = "greet")]
    pub(crate) fn duplicate(&self) -> String { String::new() }
}
"#,
        );
        document.digest = Sha256Digest::hash_bytes(document.contents.as_bytes());
        self.digest = source_snapshot_digest(&self).expect("mutated snapshot must hash");
        self
    }
}

fn target(visible_schema_digest: Sha256Digest) -> ModuleTarget {
    ModuleTarget {
        dagger_revision: value("0123456789abcdef0123456789abcdef01234567"),
        engine_version: value("v1.0.0"),
        rust_sdk_version: value("1.0.0-beta.10"),
        rust_toolchain: value("1.89.0"),
        rust_edition: value("2024"),
        visible_schema_digest,
    }
}

fn observed(compilation: &ModuleCompilation) -> BTreeMap<GeneratedAssetPath, Sha256Digest> {
    compilation
        .assets
        .manifest
        .assets
        .iter()
        .map(|(path, asset)| (path.clone(), asset.digest.clone()))
        .collect()
}

fn digest(value: impl AsRef<[u8]>) -> Sha256Digest {
    Sha256Digest::hash_bytes(value.as_ref())
}

fn value(value: impl Into<String>) -> TargetValue {
    TargetValue::new(value).expect("fixture target value is valid")
}
