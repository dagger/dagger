//! Generated compile and query-projection verification metadata.
//!
//! Verification is derived from the same immutable projection plan as source, but its
//! public-symbol inventory is recovered from parsed candidate syntax. Equality between
//! the independently recovered and deliberately referenced sets prevents either the
//! renderer or its generated test program from silently forgetting a public item.

use std::collections::{BTreeMap, BTreeSet};

use serde::Serialize;

use crate::ProjectionPlan;
use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::projection::catalog::{BindingKey, BindingKind};
use crate::projection::fields::{ArgumentPresence, FieldStrategy, InputEncoder};
use crate::schema::canonical::SchemaCoordinate;

/// Exhaustive verification data paired with one rendered candidate.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct GeneratedVerification {
    public_symbols: BTreeSet<String>,
    referenced_symbols: BTreeSet<String>,
    binding_tests: BTreeMap<BindingKey, String>,
    query_cases: BTreeMap<SchemaCoordinate, QueryProjectionCase>,
    omission_cases: BTreeSet<RequiredOmissionCase>,
}

impl GeneratedVerification {
    /// Returns every public symbol recovered from parsed candidate source.
    #[must_use]
    pub const fn public_symbols(&self) -> &BTreeSet<String> {
        &self.public_symbols
    }

    /// Returns every public symbol named by the generated reachability program.
    #[must_use]
    pub const fn referenced_symbols(&self) -> &BTreeSet<String> {
        &self.referenced_symbols
    }

    /// Returns the generated test entry covering every semantic binding.
    #[must_use]
    pub const fn binding_tests(&self) -> &BTreeMap<BindingKey, String> {
        &self.binding_tests
    }

    /// Returns one structured projection case for every public target field.
    #[must_use]
    pub const fn query_cases(&self) -> &BTreeMap<SchemaCoordinate, QueryProjectionCase> {
        &self.query_cases
    }

    /// Returns every required method argument and input field omission contract.
    #[must_use]
    pub const fn omission_cases(&self) -> &BTreeSet<RequiredOmissionCase> {
        &self.omission_cases
    }
}

/// One exact field and argument projection contract used by generated tests.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct QueryProjectionCase {
    /// Exact field coordinate.
    pub coordinate: SchemaCoordinate,
    /// Owning GraphQL type Wire_Name.
    pub owner_wire_name: String,
    /// Exact selected field Wire_Name.
    pub field_wire_name: String,
    /// Generated Rust method identifier.
    pub rust_method_name: String,
    /// Complete field execution strategy.
    pub strategy: FieldStrategy,
    /// Exact arguments in deterministic Wire_Name order.
    pub arguments: Vec<QueryArgumentCase>,
}

/// One exact argument projection contract used by generated tests.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct QueryArgumentCase {
    /// Exact argument coordinate.
    pub coordinate: SchemaCoordinate,
    /// GraphQL argument Wire_Name emitted into documents.
    pub wire_name: String,
    /// Generated Rust identifier, retained independently from the Wire_Name.
    pub rust_name: String,
    /// Required direct input or options-carried omission policy.
    pub presence: ArgumentPresence,
    /// Recursive concrete-value or lazy-ID encoder.
    pub encoder: InputEncoder,
}

/// A required construction path that generated negative compile tests must cover.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct RequiredOmissionCase {
    /// Exact omitted schema coordinate.
    pub coordinate: SchemaCoordinate,
    /// Public construction surface on which omission must fail.
    pub kind: RequiredOmissionKind,
    /// Relevant generated public name used to stabilize compiler evidence.
    pub public_name: String,
}

/// The two compile-time requiredness surfaces.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum RequiredOmissionKind {
    /// A required generated method argument.
    MethodArgument,
    /// A required generated input-object constructor field.
    InputField,
}

pub(crate) fn assemble(
    plan: &ProjectionPlan,
    public_symbols: BTreeSet<String>,
    referenced_symbols: BTreeSet<String>,
) -> Result<GeneratedVerification, DiagnosticSet> {
    if public_symbols != referenced_symbols {
        let missing = public_symbols
            .difference(&referenced_symbols)
            .next()
            .or_else(|| referenced_symbols.difference(&public_symbols).next())
            .map(String::as_str)
            .unwrap_or("generated public namespace");
        return Err(DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::CapabilityBindingMissing,
            Some(DiagnosticCoordinate::new(missing)),
            "generated reachability symbols do not equal parsed public symbols",
        )));
    }

    let binding_tests = plan
        .catalog()
        .bindings()
        .keys()
        .cloned()
        .map(|key| {
            let test = match key.binding_kind {
                BindingKind::QueryRoot
                | BindingKind::Scalar
                | BindingKind::ObjectHandle
                | BindingKind::InterfaceTrait
                | BindingKind::InterfaceClient
                | BindingKind::InterfaceImplementation
                | BindingKind::Enum
                | BindingKind::EnumVariant
                | BindingKind::InputObject
                | BindingKind::InputField
                | BindingKind::FieldOptions => "generated_public_reachability",
                BindingKind::FieldOperation | BindingKind::Argument => "generated_query_projection",
                BindingKind::EnumAlias
                | BindingKind::TargetPrivateType
                | BindingKind::TargetPrivateField
                | BindingKind::DirectivePolicy
                | BindingKind::DirectiveArgument => "generated_projection_policy",
            };
            (key, test.to_owned())
        })
        .collect();

    let query_cases = plan
        .fields()
        .values()
        .map(|field| {
            (
                field.coordinate.clone(),
                QueryProjectionCase {
                    coordinate: field.coordinate.clone(),
                    owner_wire_name: field.owner.to_string(),
                    field_wire_name: field.wire_name.to_string(),
                    rust_method_name: field.rust_name.clone(),
                    strategy: field.strategy.clone(),
                    arguments: field
                        .arguments
                        .iter()
                        .map(|argument| QueryArgumentCase {
                            coordinate: argument.coordinate.clone(),
                            wire_name: argument.wire_name.to_string(),
                            rust_name: argument.rust_name.clone(),
                            presence: argument.presence.clone(),
                            encoder: argument.encoder.clone(),
                        })
                        .collect(),
                },
            )
        })
        .collect();

    let mut omission_cases = BTreeSet::new();
    for field in plan.fields().values() {
        if matches!(field.strategy, FieldStrategy::TargetPrivate) {
            continue;
        }
        for argument in &field.arguments {
            if argument.presence == ArgumentPresence::Required {
                omission_cases.insert(RequiredOmissionCase {
                    coordinate: argument.coordinate.clone(),
                    kind: RequiredOmissionKind::MethodArgument,
                    public_name: format!("{}::{}", field.owner, field.rust_name),
                });
            }
        }
    }
    for projection in plan.named_types().values() {
        if let crate::projection::types::TypeProjection::InputObject(input) = projection {
            for field in input.fields.values() {
                if field.presence == ArgumentPresence::Required {
                    omission_cases.insert(RequiredOmissionCase {
                        coordinate: field.coordinate.clone(),
                        kind: RequiredOmissionKind::InputField,
                        public_name: format!("{}::{}", input.rust_name, input.constructor_name),
                    });
                }
            }
        }
    }

    Ok(GeneratedVerification {
        public_symbols,
        referenced_symbols,
        binding_tests,
        query_cases,
        omission_cases,
    })
}
