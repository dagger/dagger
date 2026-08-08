//! Shared valid-first generators and deterministic recording doubles for codegen tests.

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};

use dagger_codegen::projection::catalog::{CatalogDisposition, CatalogEntry};
use dagger_codegen::schema::raw::{
    DirectiveApplication, DirectiveApplicationArgument, FullType, TypeKind, TypeRef,
};
use dagger_codegen::target::CodegenTarget;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

pub(crate) const PURE_CASES: u32 = 256;
pub(crate) const FILESYSTEM_CASES: u32 = 128;

pub(crate) fn pure_config() -> Config {
    Config {
        cases: PURE_CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/foundations.txt"
        )))),
        ..Config::default()
    }
}

pub(crate) fn filesystem_config() -> Config {
    Config {
        cases: FILESYSTEM_CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/filesystem.txt"
        )))),
        ..Config::default()
    }
}

pub(crate) fn name_strategy() -> impl Strategy<Value = String> {
    "[A-Za-z_][A-Za-z0-9_]{0,31}"
}

pub(crate) fn default_literal_strategy() -> impl Strategy<Value = String> {
    prop_oneof![
        Just("null".to_owned()),
        any::<bool>().prop_map(|value| value.to_string()),
        any::<i32>().prop_map(|value| value.to_string()),
        "[A-Za-z0-9 _-]{0,24}".prop_map(|value| format!("\"{value}\"")),
    ]
}

pub(crate) fn target_strategy() -> impl Strategy<Value = CodegenTarget> {
    Just(
        CodegenTarget::decode_exact(include_bytes!("../../../../completeness/target.json"))
            .expect("checked target must decode"),
    )
}

pub(crate) fn wrapper_strategy() -> impl Strategy<Value = TypeRef> {
    let leaf = name_strategy().prop_map(|name| TypeRef {
        kind: Some(TypeKind::Scalar),
        name: Some(name),
        of_type: None,
        interfaces: None,
        possible_types: None,
        directives: None,
        unknown_fields: BTreeMap::new(),
    });
    leaf.prop_recursive(8, 64, 2, |inner| {
        prop_oneof![
            inner.clone().prop_map(|inner| TypeRef {
                kind: Some(TypeKind::List),
                name: None,
                of_type: Some(Box::new(inner)),
                interfaces: None,
                possible_types: None,
                directives: None,
                unknown_fields: BTreeMap::new(),
            }),
            inner.prop_map(|inner| TypeRef {
                kind: Some(TypeKind::NonNull),
                name: None,
                of_type: Some(Box::new(inner)),
                interfaces: None,
                possible_types: None,
                directives: None,
                unknown_fields: BTreeMap::new(),
            }),
        ]
    })
}

pub(crate) fn directive_strategy() -> impl Strategy<Value = DirectiveApplication> {
    (
        name_strategy(),
        prop::collection::vec((name_strategy(), default_literal_strategy()), 0..4),
    )
        .prop_map(|(name, arguments)| DirectiveApplication {
            name,
            args: arguments
                .into_iter()
                .map(|(name, value)| DirectiveApplicationArgument {
                    name,
                    value: Some(value),
                    unknown_fields: BTreeMap::new(),
                })
                .collect(),
            unknown_fields: BTreeMap::new(),
        })
}

pub(crate) fn raw_schema_fragment_strategy() -> impl Strategy<Value = FullType> {
    (
        name_strategy(),
        prop::option::of(".{0,32}"),
        prop::collection::vec(directive_strategy(), 0..3),
    )
        .prop_map(|(name, description, _directives)| FullType {
            kind: Some(TypeKind::Scalar),
            name: Some(name),
            description,
            fields: None,
            input_fields: None,
            interfaces: None,
            enum_values: None,
            possible_types: None,
            directives: None,
            unknown_fields: BTreeMap::new(),
        })
}

pub(crate) fn canonical_schema_fragment_strategy() -> impl Strategy<Value = String> {
    name_strategy().prop_filter("canonical fragments exclude introspection names", |name| {
        !name.starts_with("__")
    })
}

pub(crate) fn catalog_strategy() -> impl Strategy<Value = Vec<CatalogEntry>> {
    prop::collection::btree_map(
        name_strategy(),
        prop_oneof![
            Just(CatalogDisposition::Emitted),
            Just(CatalogDisposition::RuntimeProvided),
            Just(CatalogDisposition::PolicyRecorded),
        ],
        0..24,
    )
    .prop_map(|entries| {
        entries
            .into_iter()
            .map(|(schema_id, disposition)| CatalogEntry {
                schema_id,
                disposition,
                reason: None,
            })
            .collect()
    })
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct ArtifactSet(pub BTreeMap<PathBuf, Vec<u8>>);

pub(crate) fn artifact_set_strategy() -> impl Strategy<Value = ArtifactSet> {
    prop::collection::btree_map(
        "[a-z][a-z0-9_-]{0,12}\\.rs",
        prop::collection::vec(any::<u8>(), 0..64),
        0..8,
    )
    .prop_map(|entries| {
        ArtifactSet(
            entries
                .into_iter()
                .map(|(path, bytes)| (path.into(), bytes))
                .collect(),
        )
    })
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct CapabilityMapping {
    pub capability_id: String,
    pub schema_id: String,
}

pub(crate) fn capability_mapping_strategy() -> impl Strategy<Value = CapabilityMapping> {
    (name_strategy(), name_strategy()).prop_map(|(capability_id, schema_id)| CapabilityMapping {
        capability_id: format!("schema/engine-schema/{capability_id}"),
        schema_id,
    })
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct EvidenceRecord {
    pub subject: String,
    pub digest: [u8; 32],
}

pub(crate) fn evidence_record_strategy() -> impl Strategy<Value = EvidenceRecord> {
    (name_strategy(), any::<[u8; 32]>())
        .prop_map(|(subject, digest)| EvidenceRecord { subject, digest })
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingSelection {
    pub fields: Vec<String>,
}

impl RecordingSelection {
    pub(crate) fn select(&mut self, wire_name: impl Into<String>) {
        self.fields.push(wire_name.into());
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingSession {
    pub executions: Vec<Vec<String>>,
}

impl RecordingSession {
    pub(crate) fn execute(&mut self, selection: &RecordingSelection) {
        self.executions.push(selection.fields.clone());
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingFormatter {
    pub candidates: Vec<Vec<u8>>,
}

impl RecordingFormatter {
    pub(crate) fn format(&mut self, candidate: &[u8]) -> Vec<u8> {
        self.candidates.push(candidate.to_vec());
        candidate.to_vec()
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingFilesystem {
    pub files: BTreeMap<PathBuf, Vec<u8>>,
    pub events: Vec<String>,
}

impl RecordingFilesystem {
    pub(crate) fn write(&mut self, path: impl Into<PathBuf>, bytes: Vec<u8>) {
        let path = path.into();
        self.events.push(format!("write:{}", path.display()));
        self.files.insert(path, bytes);
    }

    pub(crate) fn read(&mut self, path: &Path) -> Option<Vec<u8>> {
        self.events.push(format!("read:{}", path.display()));
        self.files.get(path).cloned()
    }
}

#[derive(Clone, Debug, Default)]
pub(crate) struct RecordingPublicationLock {
    held: Arc<AtomicBool>,
    acquisitions: Arc<AtomicUsize>,
}

impl RecordingPublicationLock {
    pub(crate) fn try_acquire(&self) -> Option<RecordingPublicationGuard> {
        self.held
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .ok()
            .map(|_| {
                self.acquisitions.fetch_add(1, Ordering::Relaxed);
                RecordingPublicationGuard {
                    held: Arc::clone(&self.held),
                }
            })
    }

    pub(crate) fn acquisitions(&self) -> usize {
        self.acquisitions.load(Ordering::Relaxed)
    }
}

#[derive(Debug)]
pub(crate) struct RecordingPublicationGuard {
    held: Arc<AtomicBool>,
}

impl Drop for RecordingPublicationGuard {
    fn drop(&mut self) {
        self.held.store(false, Ordering::Release);
    }
}
