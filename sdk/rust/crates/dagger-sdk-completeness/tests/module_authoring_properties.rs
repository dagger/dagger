//! Engine-free properties for module scope, package graph, grammar, and wire models.

#[path = "support/module_authoring.rs"]
mod module_authoring_support;

use std::collections::{BTreeMap, BTreeSet};
use std::num::NonZeroU32;

use dagger_codegen::module::{
    ArgumentMetadata, AuthoringAbi, AuthoringFieldPolicy, AuthoringFingerprintValue,
    AuthoringParser, CachePolicy, CfgEnvironment, CompiledArgument, CompiledFunction,
    ConstructionPolicy, DescriptorProvenance, DigestDomain as ModuleDigestDomain,
    DispatchCoordinate, ExecutionKind, FormatVersion, FunctionDescriptor, FunctionKind,
    FunctionMetadata, FunctionReturn, FunctionRole, GeneratedAsset, GeneratedAssetOwner,
    GeneratedAssetPath, GeneratedModuleAssets, LocalTypeContract, LocalTypeDescriptor,
    LocalTypeKind, ModuleDescriptor, ModuleIntrospection, ModulePackage, ModuleSourcePath,
    ModuleSourceSnapshot, ModuleTarget, ObjectContract, PackageName, ProjectedTypeDef,
    ProjectedTypeKind, ReceiverKind, RegenerationClass, RegistrationProjection, RustModuleType,
    RustSymbol, Sha256Digest, SourceCoordinate, SourceDocument, TargetValue, WireName,
    canonical_bytes as module_canonical_bytes, canonical_digest as module_canonical_digest,
    decode_canonical as decode_module_canonical,
};
use dagger_sdk::__private::{
    CallEnvelope, CallIdentity, CallSelector, ModuleJson, ModuleWireName, NamedModuleArgument,
};
use dagger_sdk_completeness::{
    CanonicalSet, Digest, EvidenceId, ModuleAuthoringFormatVersion, ModuleAuthority,
    ModuleEvidenceDomain, ModuleEvidenceObservation, ModuleEvidenceOutcome, TargetDigest,
    admit_module_authoring_evidence, derive_module_authoring_scope, module_authoring_scope_input,
};
use module_authoring_support::{
    dependency_alias_strategy, filesystem_concurrency_config,
    filesystem_concurrency_shape_strategy, pure_config, source_path_strategy,
};
use proptest::prelude::*;
use proptest::test_runner::TestRunner;
use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::json;

proptest! {
    #![proptest_config(pure_config())]

    // Exact scope derivation is all-or-nothing, and rejected evidence cannot hide a blocker.
    #[test]
    fn property_01_capability_scope_exact_evidence_local(
        seed in any::<u8>(),
        scope_mutation in 0_u8..19,
        evidence_mutation in 0_u8..9,
        reverse_order in any::<bool>(),
    ) {
        let target = target_digest(seed);
        let mut input = module_authoring_scope_input(target.clone());
        let canonical_digest = derive_module_authoring_scope(&input, &target).unwrap().mapping_digest().clone();
        if reverse_order {
            input.mappings.reverse();
            input.ownership_corrections.reverse();
        }
        let mapping_index = usize::from(seed) % input.mappings.len();
        let correction_index = usize::from(seed) % input.ownership_corrections.len();
        match scope_mutation {
            0 => {}
            1 => { input.mappings.pop(); }
            2 => input.mappings.push(input.mappings[usize::from(seed) % input.mappings.len()].clone()),
            3 => input.mappings[mapping_index].target_digest = target_digest(seed.wrapping_add(1)),
            4 => input.mappings[mapping_index].blocker = false,
            5 => input.mappings[mapping_index].authority = match input.mappings[mapping_index].authority {
                ModuleAuthority::GoCodegen => ModuleAuthority::GoClient,
                _ => ModuleAuthority::GoCodegen,
            },
            6 => { input.ownership_corrections.pop(); }
            7 => input.ownership_corrections.push(input.ownership_corrections[usize::from(seed) % input.ownership_corrections.len()].clone()),
            8 => input.ownership_corrections[correction_index].to = dagger_sdk_completeness::FeatureId::Feature6,
            9 => input.target_digest = target_digest(seed.wrapping_add(1)),
            10 => input.existing_scope_digest = Digest::sha256([seed, 10]),
            11 => input.mappings[mapping_index].capability_id = dagger_sdk_completeness::CapabilityId::new("policy/rust-policy/module-catch-all").unwrap(),
            12 => input.mappings[mapping_index].minimum_evidence_domain = ModuleEvidenceDomain::SiblingStandaloneClient,
            13 => input.ownership_corrections[correction_index].from = dagger_sdk_completeness::FeatureId::Feature5,
            14 => input.mappings[mapping_index].requirement = dagger_sdk_completeness::NonEmptyText::new("17.18").unwrap(),
            15 => input.mappings[mapping_index].implementation_subject = match input.mappings[mapping_index].implementation_subject {
                dagger_sdk_completeness::ModuleImplementationSubject::GeneratedAssets => dagger_sdk_completeness::ModuleImplementationSubject::SourceCompiler,
                _ => dagger_sdk_completeness::ModuleImplementationSubject::GeneratedAssets,
            },
            16 => input.mappings[mapping_index].allowed_terminal_status = dagger_sdk_completeness::ModuleTerminalStatus::Inapplicable,
            17 => input.mappings[mapping_index].minimum_evidence_domain = match input.mappings[mapping_index].minimum_evidence_domain {
                ModuleEvidenceDomain::CompileFixture => ModuleEvidenceDomain::CompilerProperty,
                _ => ModuleEvidenceDomain::CompileFixture,
            },
            18 => input.mappings[mapping_index].rationale = dagger_sdk_completeness::NonEmptyText::new("unreviewed rationale").unwrap(),
            _ => unreachable!(),
        }

        let derived = derive_module_authoring_scope(&input, &target);
        if scope_mutation != 0 {
            prop_assert!(derived.is_err());
            return Ok(());
        }
        let scope = derived.unwrap();
        prop_assert_eq!(scope.mapping_digest(), &canonical_digest);
        prop_assert_eq!(scope.mappings().len(), 111);
        prop_assert_eq!(scope.ownership_corrections().len(), 17);

        let mapping = scope.mappings().values().nth(usize::from(seed) % scope.mappings().len()).unwrap();
        let mut observation = ModuleEvidenceObservation {
            format_version: ModuleAuthoringFormatVersion::current(),
            evidence_id: EvidenceId::new(format!("module/property-{seed}")).unwrap(),
            target_digest: target.clone(),
            mapping_digest: scope.mapping_digest().clone(),
            domain: mapping.minimum_evidence_domain,
            capability_ids: CanonicalSet::new([mapping.capability_id.clone()]),
            result: ModuleEvidenceOutcome::Passed { observation_digest: Digest::sha256([seed, 1]) },
        };
        match evidence_mutation {
            0 => {}
            1 => observation.target_digest = target_digest(seed.wrapping_add(1)),
            2 => observation.mapping_digest = Digest::sha256([seed, 2]),
            3 => observation.result = ModuleEvidenceOutcome::Failed { diagnostic: dagger_sdk_completeness::NonEmptyText::new("fixture failed").unwrap() },
            4 => observation.result = ModuleEvidenceOutcome::Skipped { reason: dagger_sdk_completeness::NonEmptyText::new("fixture skipped").unwrap() },
            5 => observation.capability_ids = CanonicalSet::new([dagger_sdk_completeness::CapabilityId::new("policy/rust-policy/sibling-client").unwrap()]),
            6 => observation.domain = ModuleEvidenceDomain::SiblingStandaloneClient,
            7 => observation.capability_ids = CanonicalSet::default(),
            8 => observation.domain = ModuleEvidenceDomain::LifecycleIntegration,
            _ => unreachable!(),
        }
        let admission = admit_module_authoring_evidence(&scope, &observation);
        if evidence_mutation == 0 {
            prop_assert_eq!(admission.status_changes.len(), 1);
            prop_assert_eq!(admission.blockers.len(), 110);
            prop_assert!(admission.rejection.is_none());
        } else {
            prop_assert!(admission.status_changes.is_empty());
            prop_assert_eq!(admission.blockers.len(), 111);
            prop_assert!(admission.rejection.is_some());
        }
    }

    // Visibility remains a Rust concern; only explicit accessible marks enter discovery.
    #[test]
    fn property_02_export_explicit_preserves_rust_visibility(
        visibility in 0_u8..3,
        marked in any::<bool>(),
        field_policy in 0_u8..3,
        callable_kind in 0_u8..3,
        seed in any::<u16>(),
    ) {
        let visibility_text = match visibility {
            0 => "pub",
            1 => "pub(crate)",
            _ => "",
        };
        let outer = if marked { "#[sdk_alias::object(root)]" } else { "" };
        let field = match field_policy {
            0 => "#[dagger(field)]",
            1 => "#[dagger(state)]",
            _ => "",
        };
        let callable = match callable_kind {
            0 => "fn value(&self) -> String { self.value.clone() }",
            1 => {
                "#[dagger(function)]\nfn value(&self) -> String { self.value.clone() }"
            }
            _ => {
                "#[dagger(constructor)]\nfn value(value: String) -> Self { Self { value } }"
            }
        };
        let source = format!(r#"
            {outer}
            {visibility_text} struct Item{seed} {{
                {field}
                value: String,
            }}

            #[sdk_alias::methods]
            impl Item{seed} {{
                {callable}
            }}

            pub struct OrdinaryPublic;
        "#);
        let path = ModuleSourcePath::new("src/lib.rs").unwrap();
        let result = AuthoringParser::parse(&path, &source);
        if marked && visibility == 2 {
            prop_assert!(result.is_err());
            return Ok(());
        }
        let declarations = result.unwrap();
        prop_assert_eq!(declarations.len(), usize::from(marked));
        if let Some(declaration) = declarations.first() {
            prop_assert_eq!(declaration.fields.len(), 1);
            let expected = match field_policy {
                0 => AuthoringFieldPolicy::Field,
                1 => AuthoringFieldPolicy::State,
                _ => AuthoringFieldPolicy::Transient,
            };
            prop_assert_eq!(declaration.fields[0].policy, expected);
            prop_assert_eq!(declaration.functions.len(), usize::from(callable_kind != 0));
        }
    }

    // Alias spelling and metadata order do not change the shared source interpretation.
    #[test]
    fn property_03_source_macro_interpretations_converge(
        root_first in any::<bool>(),
        use_alias in any::<bool>(),
        semantic_mutation in any::<bool>(),
        seed in any::<u16>(),
    ) {
        let alias = if use_alias { "renamed_sdk" } else { "dagger_sdk" };
        let rename = if semantic_mutation { format!("item_{seed}_changed") } else { format!("item_{seed}") };
        let args = if root_first {
            format!("root, rename = \"{rename}\"")
        } else {
            format!("rename = \"{rename}\", root")
        };
        let source = format!(r#"
            #[{alias}::object({args})]
            pub(crate) struct Item{seed} {{
                #[dagger(field)]
                value: String,
            }}
        "#);
        let path = ModuleSourcePath::new("src/lib.rs").unwrap();
        let declaration = AuthoringParser::parse(&path, &source).unwrap().remove(0);
        prop_assert_eq!(
            declaration.fingerprint.as_u128().unwrap(),
            reference_object_fingerprint(&format!("Item{seed}"), &rename),
        );

        let reference = format!(r#"
            #[dagger_sdk::object(rename = "item_{seed}", root)]
            pub(crate) struct Item{seed} {{
                #[dagger(field)]
                value: String,
            }}
        "#);
        let reference = AuthoringParser::parse(&path, &reference).unwrap().remove(0);
        if semantic_mutation {
            prop_assert_ne!(declaration.fingerprint, reference.fingerprint);
        } else {
            prop_assert_eq!(declaration.fingerprint, reference.fingerprint);
            prop_assert_eq!(declaration.fields, reference.fields);
        }

        let malformed = format!("#[{alias}::object(root, root)] pub struct Broken{seed} {{}}");
        let error = AuthoringParser::parse(&path, &malformed).unwrap_err();
        let diagnostic = &error.diagnostics()[0];
        let first_root = malformed.find("root").unwrap();
        let second_root = malformed[first_root + 4..].find("root").unwrap() + first_root + 4;
        prop_assert_eq!(diagnostic.code(), dagger_codegen::module::ModuleDiagnosticCode::MetadataConflict);
        let coordinate = diagnostic.source_coordinate().unwrap();
        prop_assert_eq!(coordinate.line.get(), 1);
        prop_assert_eq!(coordinate.column.get() as usize, second_root + 1);
    }

    // The public graph admits only one exact-version acyclic SDK/macro pair.
    #[test]
    fn property_31_public_package_graph_closed_version_coherent(
        alias in dependency_alias_strategy(),
        mutation in 0_u8..14,
        seed in any::<u8>(),
    ) {
        let mut graph = PackageGraph::valid(alias);
        match mutation {
            0 => {}
            1 => graph.sdk.version = "1.0.0-beta.11".to_owned(),
            2 => graph.macros.edition = "2021".to_owned(),
            3 => graph.macros.rust_version = "1.96.0".to_owned(),
            4 => graph.macros.repository = "https://github.com/acme/dagger".to_owned(),
            5 => graph.macros.license = "MIT".to_owned(),
            6 => graph.sdk_to_macros_exact = false,
            7 => graph.macros_to_sdk = true,
            8 => graph.private_reachable = true,
            9 => graph.engine_checkout_path = true,
            10 => { graph.sdk.features.insert(format!("unexpected-{seed}")); }
            11 => { graph.macros.features.insert("runtime".to_owned()); }
            12 => graph.macros.publish = false,
            13 => graph.sdk_to_macros_source_coherent = false,
            _ => unreachable!(),
        }
        prop_assert_eq!(validate_package_graph(&graph), mutation == 0);
    }

    // Every canonical authoring wire family has one strict byte spelling and digest.
    #[test]
    fn property_32_canonical_wire_models_round_trip_without_semantic_loss(
        seed in any::<u8>(),
        model_kind in 0_u8..8,
        invalid_kind in 0_u8..7,
        source_path in source_path_strategy(),
    ) {
        match model_kind {
            0 => assert_round_trip(ModuleDigestDomain::SourceSnapshot, snapshot(seed, source_path.clone())),
            1 => assert_round_trip(ModuleDigestDomain::ModuleDescriptor, descriptor(seed)),
            2 => assert_round_trip(ModuleDigestDomain::Registration, registration(seed)),
            3 => assert_round_trip(ModuleDigestDomain::Introspection, introspection(seed)),
            4 => assert_round_trip(ModuleDigestDomain::GeneratedAssets, assets(seed)),
            5 => assert_round_trip(ModuleDigestDomain::CallEnvelope, call(seed)),
            6 => assert_round_trip(ModuleDigestDomain::Evidence, module_authoring_scope_input(target_digest(seed))),
            7 => assert_round_trip(ModuleDigestDomain::Evidence, evidence(seed)),
            _ => unreachable!(),
        }

        match invalid_kind {
            0 => {
                let mut value = serde_json::to_value(snapshot(seed, source_path.clone())).unwrap();
                value.as_object_mut().unwrap().insert("unknown".to_owned(), json!(true));
                prop_assert!(decode_json_value::<ModuleSourceSnapshot>(value).is_err());
            }
            1 => {
                let mut value = serde_json::to_value(snapshot(seed, source_path.clone())).unwrap();
                let documents = value["documents"].as_object_mut().unwrap();
                let document = documents.values_mut().next().unwrap();
                document["path"] = json!("../escape.rs");
                prop_assert!(decode_json_value::<ModuleSourceSnapshot>(value).is_err());
            }
            2 => {
                let mut value = serde_json::to_value(descriptor(seed)).unwrap();
                value["digest"] = json!("sha256:ABCDEF");
                prop_assert!(decode_json_value::<ModuleDescriptor>(value).is_err());
            }
            3 => {
                let mut value = serde_json::to_value(snapshot(seed, source_path.clone())).unwrap();
                value["format_version"] = json!(2);
                prop_assert!(decode_json_value::<ModuleSourceSnapshot>(value).is_err());
            }
            4 => {
                let mut value = serde_json::to_value(descriptor(seed)).unwrap();
                value["authoring_abi"] = json!(2);
                prop_assert!(decode_json_value::<ModuleDescriptor>(value).is_err());
            }
            5 => {
                let bytes = serde_json::to_vec(&snapshot(seed, source_path)).unwrap();
                prop_assert!(decode_module_canonical::<ModuleSourceSnapshot>(&bytes).is_err());
            }
            6 => {
                let mut value = serde_json::to_value(evidence(seed)).unwrap();
                value["domain"] = json!("unknown-domain");
                prop_assert!(decode_json_value::<ModuleEvidenceObservation>(value).is_err());
            }
            _ => unreachable!(),
        }
    }
}

#[test]
fn filesystem_and_concurrency_strategies_are_valid_first_and_bounded() {
    let mut runner = TestRunner::new(filesystem_concurrency_config());
    runner
        .run(
            &filesystem_concurrency_shape_strategy(),
            |(paths, schedule)| {
                prop_assert!(!paths.is_empty() && paths.len() < 16);
                prop_assert!(schedule.len() < 32);
                prop_assert!(
                    paths
                        .iter()
                        .all(|path| ModuleSourcePath::new(path.as_str()).is_ok())
                );
                Ok(())
            },
        )
        .expect("valid-first filesystem/concurrency strategy remains within its bounds");
}

#[derive(Clone, Debug)]
struct Package {
    version: String,
    edition: String,
    rust_version: String,
    repository: String,
    license: String,
    publish: bool,
    features: BTreeSet<String>,
}

#[derive(Clone, Debug)]
struct PackageGraph {
    sdk: Package,
    macros: Package,
    dependency_alias: String,
    sdk_to_macros_exact: bool,
    sdk_to_macros_source_coherent: bool,
    macros_to_sdk: bool,
    private_reachable: bool,
    engine_checkout_path: bool,
}

impl PackageGraph {
    fn valid(dependency_alias: String) -> Self {
        let metadata = Package {
            version: "1.0.0-beta.10".to_owned(),
            edition: "2024".to_owned(),
            rust_version: "1.97.1".to_owned(),
            repository: "https://github.com/dagger/dagger".to_owned(),
            license: "Apache-2.0".to_owned(),
            publish: true,
            features: BTreeSet::new(),
        };
        let mut sdk = metadata.clone();
        sdk.features.insert("default".to_owned());
        sdk.features.insert("gen".to_owned());
        Self {
            sdk,
            macros: metadata,
            dependency_alias,
            sdk_to_macros_exact: true,
            sdk_to_macros_source_coherent: true,
            macros_to_sdk: false,
            private_reachable: false,
            engine_checkout_path: false,
        }
    }
}

fn validate_package_graph(graph: &PackageGraph) -> bool {
    let metadata_matches = graph.sdk.version == graph.macros.version
        && graph.sdk.edition == graph.macros.edition
        && graph.sdk.rust_version == graph.macros.rust_version
        && graph.sdk.repository == graph.macros.repository
        && graph.sdk.license == graph.macros.license;
    metadata_matches
        && graph.sdk.publish
        && graph.macros.publish
        && graph.sdk_to_macros_exact
        && graph.sdk_to_macros_source_coherent
        && !graph.macros_to_sdk
        && !graph.private_reachable
        && !graph.engine_checkout_path
        && !graph.dependency_alias.is_empty()
        && graph.sdk.features == BTreeSet::from(["default".to_owned(), "gen".to_owned()])
        && graph.macros.features.is_empty()
}

fn assert_round_trip<T>(domain: ModuleDigestDomain, value: T)
where
    T: Clone + std::fmt::Debug + Eq + Serialize + DeserializeOwned,
{
    let bytes = module_canonical_bytes(&value).unwrap();
    let decoded = decode_module_canonical::<T>(&bytes).unwrap();
    assert_eq!(decoded, value);
    assert_eq!(
        module_canonical_digest(domain, &decoded).unwrap(),
        module_canonical_digest(domain, &value).unwrap()
    );
}

fn decode_json_value<T>(
    value: serde_json::Value,
) -> Result<T, dagger_codegen::module::CanonicalError>
where
    T: Serialize + DeserializeOwned,
{
    decode_module_canonical(&module_canonical_bytes(&value).unwrap())
}

fn target_digest(seed: u8) -> TargetDigest {
    TargetDigest::new(Digest::sha256([seed, 0x54]))
}

fn digest(seed: u8, domain: u8) -> Sha256Digest {
    Sha256Digest::hash_bytes(&[seed, domain])
}

fn target(seed: u8) -> ModuleTarget {
    ModuleTarget {
        dagger_revision: TargetValue::new(format!("revision-{seed}")).unwrap(),
        engine_version: TargetValue::new("v1.0.0-beta.10").unwrap(),
        rust_sdk_version: TargetValue::new("1.0.0-beta.10").unwrap(),
        rust_toolchain: TargetValue::new("1.97.1").unwrap(),
        rust_edition: TargetValue::new("2024").unwrap(),
        visible_schema_digest: digest(seed, 1),
    }
}

fn package() -> ModulePackage {
    ModulePackage {
        name: PackageName::new("fixture-module").unwrap(),
        crate_root: ModuleSourcePath::new("src/lib.rs").unwrap(),
        edition: TargetValue::new("2024").unwrap(),
    }
}

fn coordinate() -> SourceCoordinate {
    SourceCoordinate {
        path: ModuleSourcePath::new("src/lib.rs").unwrap(),
        line: NonZeroU32::new(1).unwrap(),
        column: NonZeroU32::new(1).unwrap(),
    }
}

fn snapshot(seed: u8, path: ModuleSourcePath) -> ModuleSourceSnapshot {
    let document = SourceDocument::new(path.clone(), format!("pub struct Fixture{seed};\n"));
    ModuleSourceSnapshot {
        format_version: FormatVersion::current(),
        package: package(),
        cfg: CfgEnvironment {
            values: BTreeMap::from([(
                "target_os".to_owned(),
                BTreeSet::from(["linux".to_owned()]),
            )]),
            features: BTreeSet::from([format!("fixture-{seed}")]),
        },
        documents: BTreeMap::from([(path, document)]),
        digest: digest(seed, 2),
    }
}

fn descriptor(seed: u8) -> ModuleDescriptor {
    let root = RustSymbol::new(format!("crate::Fixture{seed}")).unwrap();
    let wire = WireName::new(format!("Fixture{seed}")).unwrap();
    let function = WireName::new("hello").unwrap();
    ModuleDescriptor {
        format_version: FormatVersion::current(),
        authoring_abi: AuthoringAbi::current(),
        target: target(seed),
        package: package(),
        module: WireName::new(format!("FixtureModule{seed}")).unwrap(),
        root: root.clone(),
        types: vec![LocalTypeDescriptor {
            rust_symbol: root.clone(),
            wire_name: wire.clone(),
            kind: LocalTypeKind::Object { root: true },
            contract: LocalTypeContract::Object(ObjectContract {
                symbol: root.clone(),
                fields: Vec::new(),
                construction: Some(ConstructionPolicy::Default),
            }),
            fields: Vec::new(),
            interface_functions: Vec::new(),
            enum_values: Vec::new(),
            documentation: Some("Fixture object.".to_owned()),
            deprecation: None,
            source: coordinate(),
            fingerprint: AuthoringFingerprintValue::from_u128(u128::from(seed) + 1),
        }],
        functions: vec![FunctionDescriptor {
            parent: root.clone(),
            rust_symbol: RustSymbol::new(format!("crate::Fixture{seed}::hello")).unwrap(),
            wire_name: function.clone(),
            compiled: CompiledFunction {
                rust_name: "hello".to_owned(),
                wire_name: function.clone(),
                kind: FunctionKind::Function,
                receiver: ReceiverKind::Shared,
                execution: ExecutionKind::Synchronous,
                inject_context: false,
                context_index: None,
                arguments: vec![CompiledArgument {
                    rust_name: "name".to_owned(),
                    wire_name: WireName::new("name").unwrap(),
                    ty: RustModuleType::String,
                    metadata: ArgumentMetadata {
                        documentation: None,
                        deprecation: None,
                        default: None,
                        default_path: None,
                        default_address: None,
                        ignore: Vec::new(),
                        optional: false,
                        source: coordinate(),
                    },
                }],
                return_type: FunctionReturn::Value(RustModuleType::String),
                metadata: FunctionMetadata {
                    documentation: None,
                    cache: CachePolicy::Default,
                    role: FunctionRole::Ordinary,
                    deprecation: None,
                    source: coordinate(),
                },
            },
            fingerprint: AuthoringFingerprintValue::from_u128(u128::from(seed) + 2),
            source: coordinate(),
        }],
        dispatch: vec![DispatchCoordinate {
            parent: wire,
            function,
        }],
        source_digest: digest(seed, 3),
        generator_digest: digest(seed, 4),
        provenance: DescriptorProvenance {
            source_files: BTreeMap::from([(
                ModuleSourcePath::new("src/lib.rs").unwrap(),
                digest(seed, 3),
            )]),
            cfg: CfgEnvironment {
                values: BTreeMap::new(),
                features: BTreeSet::new(),
            },
            visible_schema_digest: digest(seed, 1),
            generator_digest: digest(seed, 4),
            authoring_abi: AuthoringAbi::current(),
        },
        digest: digest(seed, 5),
    }
}

fn registration(seed: u8) -> RegistrationProjection {
    RegistrationProjection {
        format_version: FormatVersion::current(),
        descriptor_digest: digest(seed, 5),
        types: BTreeMap::from([projected_type(seed)]),
    }
}

fn introspection(seed: u8) -> ModuleIntrospection {
    ModuleIntrospection {
        format_version: FormatVersion::current(),
        descriptor_digest: digest(seed, 5),
        types: BTreeMap::from([projected_type(seed)]),
    }
}

fn assets(seed: u8) -> GeneratedModuleAssets {
    let path = GeneratedAssetPath::new("src/dagger_generated/mod.rs").unwrap();
    GeneratedModuleAssets {
        format_version: FormatVersion::current(),
        target: target(seed),
        descriptor_digest: digest(seed, 5),
        manifest_path: GeneratedAssetPath::new("src/dagger_generated/generated-module-assets.json")
            .unwrap(),
        assets: BTreeMap::from([(
            path.clone(),
            GeneratedAsset {
                path,
                digest: digest(seed, 6),
                owner: GeneratedAssetOwner::Descriptor,
                input_digest: digest(seed, 3),
                regeneration: RegenerationClass::Authoring,
            },
        )]),
        digest: digest(seed, 7),
    }
}

fn call(seed: u8) -> CallEnvelope {
    CallEnvelope::new(
        CallIdentity::new(
            format!("call-{seed}"),
            CallSelector::Invocation {
                parent_wire_name: ModuleWireName::new(format!("Fixture{seed}")).unwrap(),
                function_wire_name: ModuleWireName::new("hello").unwrap(),
            },
        )
        .unwrap(),
        Some(ModuleJson::new(json!({"value": seed}))),
        vec![NamedModuleArgument {
            name: ModuleWireName::new("name").unwrap(),
            value: ModuleJson::new(json!(format!("value-{seed}"))),
        }],
    )
}

fn projected_type(seed: u8) -> (WireName, ProjectedTypeDef) {
    let wire_name = WireName::new(format!("Fixture{seed}")).unwrap();
    (
        wire_name.clone(),
        ProjectedTypeDef {
            wire_name,
            kind: ProjectedTypeKind::Object,
            fields: Vec::new(),
            functions: Vec::new(),
            enum_values: Vec::new(),
            interfaces: Vec::new(),
            documentation: Some("Fixture object.".to_owned()),
            deprecation: None,
            source: coordinate(),
        },
    )
}

fn evidence(seed: u8) -> ModuleEvidenceObservation {
    let target = target_digest(seed);
    let input = module_authoring_scope_input(target.clone());
    let scope = derive_module_authoring_scope(&input, &target).unwrap();
    let mapping = scope.mappings().values().next().unwrap();
    ModuleEvidenceObservation {
        format_version: ModuleAuthoringFormatVersion::current(),
        evidence_id: EvidenceId::new(format!("module/wire-{seed}")).unwrap(),
        target_digest: target,
        mapping_digest: scope.mapping_digest().clone(),
        domain: mapping.minimum_evidence_domain,
        capability_ids: CanonicalSet::new([mapping.capability_id.clone()]),
        result: ModuleEvidenceOutcome::Passed {
            observation_digest: Digest::sha256([seed, 8]),
        },
    }
}

fn reference_object_fingerprint(type_name: &str, rename: &str) -> u128 {
    const OFFSET: u128 = 0x6c62_272e_07bb_0142_62b8_2175_6295_c58d;
    const PRIME: u128 = 0x0000_0000_0100_0000_0000_0000_0000_013b;

    let quoted_rename = format!("\"{rename}\"");
    let metadata = format!(
        "6:rename=l{}:{quoted_rename}|4:root=true",
        quoted_rename.len()
    );
    let parts = [
        "object".to_owned(),
        type_name.to_owned(),
        metadata,
        "field".to_owned(),
        "value".to_owned(),
        "i6:String".to_owned(),
        "5:field=true".to_owned(),
    ];
    let mut value = OFFSET;
    for part in parts {
        let length = u64::try_from(part.len()).unwrap_or(u64::MAX);
        for byte in length.to_le_bytes().into_iter().chain(part.bytes()) {
            value ^= u128::from(byte);
            value = value.wrapping_mul(PRIME);
        }
    }
    value
}
