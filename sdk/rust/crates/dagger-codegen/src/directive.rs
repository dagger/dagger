//! Closed projection policies for checked schema directives.
//!
//! A directive is never ignored because it is unfamiliar to the renderer. Active
//! applications become typed policy data, while inactive definitions remain pinned by
//! fingerprints and fail as soon as the target gives them client-visible semantics.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::projection::catalog::SemanticDigest;
use crate::schema::canonical::{
    CanonicalSchema, DirectiveApplication, EnumDefinition, SchemaCoordinate, SchemaName,
    TypeDefinition,
};

/// The registered policy for one target directive definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum DirectivePolicy {
    /// Typed ID inputs and self-return IDs.
    ExpectedType,
    /// Compiler deprecation plus caller-visible documentation.
    Deprecated,
    /// Caller-visible stability documentation without a feature gate.
    Experimental,
    /// A schema enum spelling that aliases a canonical sibling value.
    EnumValueAlias,
    /// Definition-only metadata contained by the exact target.
    TargetInactive,
}

impl DirectivePolicy {
    /// Returns whether checked-target applications are legal for this policy.
    #[must_use]
    pub const fn accepts_applications(&self) -> bool {
        !matches!(self, Self::TargetInactive)
    }
}

/// Typed meaning of one active directive application.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum DirectiveApplicationPolicy {
    /// Expected object or interface target.
    ExpectedType { target: SchemaName },
    /// Engine-authored deprecation reason.
    Deprecated { reason: String },
    /// Engine-authored experimental stability note.
    Experimental { reason: String },
    /// Canonical enum value selected by an alias Wire_Name.
    EnumValueAlias { canonical: SchemaName },
}

/// One definition and all of its validated target applications.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct DirectivePolicyRecord {
    /// Exact directive Wire_Name.
    pub name: SchemaName,
    /// Exact definition coordinate.
    pub coordinate: SchemaCoordinate,
    /// Semantic definition fingerprint retained for drift review.
    pub definition_fingerprint: SemanticDigest,
    /// Closed projection policy.
    pub policy: DirectivePolicy,
    /// Typed applications in exact coordinate order.
    pub applications: BTreeMap<SchemaCoordinate, DirectiveApplicationPolicy>,
}

/// Complete checked-target directive policies.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
pub struct DirectiveProjection {
    records: BTreeMap<SchemaName, DirectivePolicyRecord>,
    applications: BTreeMap<SchemaCoordinate, Vec<DirectiveApplicationPolicy>>,
}

impl DirectiveProjection {
    /// Borrows all directive records in Wire_Name order.
    #[must_use]
    pub const fn records(&self) -> &BTreeMap<SchemaName, DirectivePolicyRecord> {
        &self.records
    }

    /// Borrows all active policies attached to one schema coordinate.
    #[must_use]
    pub fn for_coordinate(&self, coordinate: &SchemaCoordinate) -> &[DirectiveApplicationPolicy] {
        self.applications
            .get(coordinate)
            .map(Vec::as_slice)
            .unwrap_or_default()
    }

    /// Returns the expected type attached to a field or argument.
    #[must_use]
    pub fn expected_type(&self, coordinate: &SchemaCoordinate) -> Option<&SchemaName> {
        self.for_coordinate(coordinate).iter().find_map(|policy| {
            if let DirectiveApplicationPolicy::ExpectedType { target } = policy {
                Some(target)
            } else {
                None
            }
        })
    }

    /// Returns the canonical value named by an enum alias coordinate.
    #[must_use]
    pub fn enum_alias(&self, coordinate: &SchemaCoordinate) -> Option<&SchemaName> {
        self.for_coordinate(coordinate).iter().find_map(|policy| {
            if let DirectiveApplicationPolicy::EnumValueAlias { canonical } = policy {
                Some(canonical)
            } else {
                None
            }
        })
    }

    /// Returns deprecation metadata attached to a public coordinate.
    #[must_use]
    pub fn deprecation_reason(&self, coordinate: &SchemaCoordinate) -> Option<&str> {
        self.for_coordinate(coordinate).iter().find_map(|policy| {
            if let DirectiveApplicationPolicy::Deprecated { reason } = policy {
                Some(reason.as_str())
            } else {
                None
            }
        })
    }

    /// Returns the experimental stability note attached to a public coordinate.
    #[must_use]
    pub fn experimental_reason(&self, coordinate: &SchemaCoordinate) -> Option<&str> {
        self.for_coordinate(coordinate).iter().find_map(|policy| {
            if let DirectiveApplicationPolicy::Experimental { reason } = policy {
                Some(reason.as_str())
            } else {
                None
            }
        })
    }
}

pub(crate) fn project_directives(
    schema: &CanonicalSchema,
) -> Result<DirectiveProjection, DiagnosticSet> {
    let sites = application_sites(schema);
    let mut records = BTreeMap::new();
    let mut applications = BTreeMap::<SchemaCoordinate, Vec<DirectiveApplicationPolicy>>::new();
    let mut diagnostics = Vec::new();

    for (name, definition) in schema.directives() {
        let policy = match name.as_str() {
            "expectedType" => DirectivePolicy::ExpectedType,
            "deprecated" => DirectivePolicy::Deprecated,
            "experimental" => DirectivePolicy::Experimental,
            "enumValue" => DirectivePolicy::EnumValueAlias,
            "cache" | "check" | "defaultAddress" | "defaultPath" | "generate"
            | "ignorePatterns" | "sourceMap" | "up" => DirectivePolicy::TargetInactive,
            _ => {
                diagnostics.push(diagnostic(
                    DiagnosticCode::SchemaDirectiveUnmapped,
                    &definition.coordinate,
                    "directive has no registered core-client projection policy",
                ));
                continue;
            }
        };
        let definition_fingerprint = match SemanticDigest::for_value(definition) {
            Ok(fingerprint) => fingerprint,
            Err(_) => {
                diagnostics.push(diagnostic(
                    DiagnosticCode::SchemaDirectiveUnmapped,
                    &definition.coordinate,
                    "directive definition could not be fingerprinted",
                ));
                continue;
            }
        };
        let mut typed = BTreeMap::new();
        for site in sites.iter().filter(|site| site.application.name == *name) {
            if !policy.accepts_applications() {
                diagnostics.push(diagnostic(
                    DiagnosticCode::TargetInactiveDirectiveChanged,
                    &site.coordinate,
                    "target-inactive directive gained a core-client application",
                ));
                continue;
            }
            match project_application(schema, site, &policy) {
                Ok(application) => {
                    typed.insert(site.coordinate.clone(), application.clone());
                    applications
                        .entry(site.coordinate.clone())
                        .or_default()
                        .push(application);
                }
                Err(error) => diagnostics.push(error),
            }
        }
        records.insert(
            name.clone(),
            DirectivePolicyRecord {
                name: name.clone(),
                coordinate: definition.coordinate.clone(),
                definition_fingerprint,
                policy,
                applications: typed,
            },
        );
    }

    for policies in applications.values_mut() {
        policies.sort();
    }
    if let Some(diagnostics) = DiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    Ok(DirectiveProjection {
        records,
        applications,
    })
}

struct ApplicationSite<'a> {
    coordinate: SchemaCoordinate,
    application: &'a DirectiveApplication,
    enum_definition: Option<&'a EnumDefinition>,
}

fn application_sites(schema: &CanonicalSchema) -> Vec<ApplicationSite<'_>> {
    let mut sites = Vec::new();
    for definition in schema.types().values() {
        match definition {
            TypeDefinition::Scalar(scalar) => {
                push_sites(&mut sites, &scalar.coordinate, &scalar.directives, None);
            }
            TypeDefinition::Object(object) => {
                push_sites(&mut sites, &object.coordinate, &object.directives, None);
                for field in object.fields.values() {
                    push_sites(&mut sites, &field.coordinate, &field.directives, None);
                    for argument in field.arguments.values() {
                        push_sites(&mut sites, &argument.coordinate, &argument.directives, None);
                    }
                }
            }
            TypeDefinition::Interface(interface) => {
                push_sites(
                    &mut sites,
                    &interface.coordinate,
                    &interface.directives,
                    None,
                );
                for field in interface.fields.values() {
                    push_sites(&mut sites, &field.coordinate, &field.directives, None);
                    for argument in field.arguments.values() {
                        push_sites(&mut sites, &argument.coordinate, &argument.directives, None);
                    }
                }
            }
            TypeDefinition::Enum(enumeration) => {
                push_sites(
                    &mut sites,
                    &enumeration.coordinate,
                    &enumeration.directives,
                    Some(enumeration),
                );
                for value in enumeration.values.values() {
                    push_sites(
                        &mut sites,
                        &value.coordinate,
                        &value.directives,
                        Some(enumeration),
                    );
                }
            }
            TypeDefinition::InputObject(input) => {
                push_sites(&mut sites, &input.coordinate, &input.directives, None);
                for field in input.fields.values() {
                    push_sites(&mut sites, &field.coordinate, &field.directives, None);
                }
            }
        }
    }
    sites
}

fn push_sites<'a>(
    sites: &mut Vec<ApplicationSite<'a>>,
    coordinate: &SchemaCoordinate,
    directives: &'a [DirectiveApplication],
    enum_definition: Option<&'a EnumDefinition>,
) {
    sites.extend(directives.iter().map(|application| ApplicationSite {
        coordinate: coordinate.clone(),
        application,
        enum_definition,
    }));
}

fn project_application(
    schema: &CanonicalSchema,
    site: &ApplicationSite<'_>,
    policy: &DirectivePolicy,
) -> Result<DirectiveApplicationPolicy, Diagnostic> {
    match policy {
        DirectivePolicy::ExpectedType => {
            let target = string_argument(site, "name", None, DiagnosticCode::ExpectedTypeInvalid)?;
            let target = SchemaName::try_from(target.as_str()).map_err(|()| {
                diagnostic(
                    DiagnosticCode::ExpectedTypeInvalid,
                    &site.coordinate,
                    "expectedType target is not a valid Wire_Name",
                )
            })?;
            if !matches!(
                schema.types().get(&target),
                Some(TypeDefinition::Object(_) | TypeDefinition::Interface(_))
            ) {
                return Err(diagnostic(
                    DiagnosticCode::ExpectedTypeInvalid,
                    &site.coordinate,
                    "expectedType target is not a public object or interface",
                ));
            }
            Ok(DirectiveApplicationPolicy::ExpectedType { target })
        }
        DirectivePolicy::Deprecated => Ok(DirectiveApplicationPolicy::Deprecated {
            reason: string_argument(
                site,
                "reason",
                Some("No longer supported"),
                DiagnosticCode::DeprecationDirectiveInvalid,
            )?,
        }),
        DirectivePolicy::Experimental => Ok(DirectiveApplicationPolicy::Experimental {
            reason: string_argument(
                site,
                "reason",
                Some("Not stabilized"),
                DiagnosticCode::ExperimentalDirectiveInvalid,
            )?,
        }),
        DirectivePolicy::EnumValueAlias => {
            let canonical = string_argument(
                site,
                "value",
                None,
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
            )?;
            let canonical = SchemaName::try_from(canonical.as_str()).map_err(|()| {
                diagnostic(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    &site.coordinate,
                    "enumValue target is not a valid Wire_Name",
                )
            })?;
            let Some(enumeration) = site.enum_definition else {
                return Err(diagnostic(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    &site.coordinate,
                    "enumValue is not attached to an enum value",
                ));
            };
            if !enumeration.values.contains_key(&canonical) {
                return Err(diagnostic(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    &site.coordinate,
                    "enumValue target is not a sibling enum Wire_Name",
                ));
            }
            let target = &enumeration.values[&canonical];
            if target
                .directives
                .iter()
                .any(|directive| directive.name.as_str() == "enumValue")
            {
                return Err(diagnostic(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    &site.coordinate,
                    "enumValue aliases may not form a chain",
                ));
            }
            Ok(DirectiveApplicationPolicy::EnumValueAlias { canonical })
        }
        DirectivePolicy::TargetInactive => Err(diagnostic(
            DiagnosticCode::TargetInactiveDirectiveChanged,
            &site.coordinate,
            "target-inactive directive gained an application",
        )),
    }
}

fn string_argument(
    site: &ApplicationSite<'_>,
    name: &str,
    default: Option<&str>,
    code: DiagnosticCode,
) -> Result<String, Diagnostic> {
    let name = SchemaName::try_from(name).map_err(|()| {
        diagnostic(
            code,
            &site.coordinate,
            "directive policy argument is invalid",
        )
    })?;
    let Some(value) = site.application.arguments.get(&name) else {
        return default
            .map(str::to_owned)
            .ok_or_else(|| diagnostic(code, &site.coordinate, "directive argument is missing"));
    };
    let Some(value) = value else {
        return default
            .map(str::to_owned)
            .ok_or_else(|| diagnostic(code, &site.coordinate, "directive argument has no value"));
    };
    serde_json::from_str::<String>(value).map_err(|_| {
        diagnostic(
            code,
            &site.coordinate,
            "directive argument is not an encoded string",
        )
    })
}

fn diagnostic(code: DiagnosticCode, coordinate: &SchemaCoordinate, message: &str) -> Diagnostic {
    Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate.as_str())),
        message,
    )
}
