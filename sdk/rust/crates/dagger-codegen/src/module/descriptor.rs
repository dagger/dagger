//! Canonical descriptor assembly from validated discovery, types, and functions.
//!
//! This phase interns every semantic item before hashing and never consults source text
//! again after the normalized models have been supplied. Equivalent input ordering
//! therefore cannot affect descriptor bytes, while every owning input domain remains
//! explicit in provenance.

use std::collections::{BTreeMap, BTreeSet};

use convert_case::{Case, Casing};
use quote::ToTokens;
use serde_json::Value;
use syn::{GenericArgument, PathArguments, Type};

use super::authoring::{AuthoringDeclaration, AuthoringDeclarationKind};
use super::canonical::{DigestDomain, canonical_digest};
use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::metadata::CompiledFunction;
use super::model::{
    AuthoringAbi, DescriptorProvenance, DispatchCoordinate, EnumValueDescriptor, FieldDescriptor,
    FunctionDescriptor, LocalTypeContract, LocalTypeDescriptor, LocalTypeKind, ModuleDescriptor,
    ModuleSourceSnapshot, ModuleTarget, ProjectedArgument, ProjectedFunction, RustSymbol,
    Sha256Digest, WireName,
};
use super::source::ModuleDiscovery;
use super::types::{ProjectedType, RustModuleType, TypeCatalog, TypePosition, TypeResolver};

/// Pure input to canonical descriptor construction.
pub struct DescriptorInput<'a> {
    /// Exact target identity.
    pub target: &'a ModuleTarget,
    /// Immutable selected source snapshot.
    pub source: &'a ModuleSourceSnapshot,
    /// Closed discovery graph.
    pub discovery: &'a ModuleDiscovery,
    /// Complete local type catalog.
    pub catalog: &'a TypeCatalog,
    /// Functions compiled and collision-checked per parent symbol.
    pub functions: &'a BTreeMap<RustSymbol, Vec<CompiledFunction>>,
    /// Owning generator identity.
    pub generator_digest: &'a Sha256Digest,
}

/// Canonical descriptor assembler.
pub struct DescriptorBuilder;

impl DescriptorBuilder {
    /// Constructs and hashes one complete descriptor or returns no partial model.
    pub fn build(input: DescriptorInput<'_>) -> Result<ModuleDescriptor, ModuleDiagnosticSet> {
        let local_names = local_wire_names(input.discovery)?;
        let mut diagnostics = Vec::new();
        let mut types = Vec::new();

        for declaration in input.discovery.declarations.values() {
            match type_descriptor(declaration, &local_names, input.discovery, input.catalog) {
                Ok(descriptor) => types.push(descriptor),
                Err(error) => diagnostics.push(error),
            }
        }
        types.sort_by(|left, right| {
            left.wire_name
                .cmp(&right.wire_name)
                .then_with(|| left.rust_symbol.cmp(&right.rust_symbol))
        });

        let mut functions = Vec::new();
        for (parent, compiled) in input.functions {
            let Some(declaration) = input.discovery.declarations.get(parent) else {
                diagnostics.push(diag(
                    ModuleDiagnosticCode::DescriptorInvalid,
                    None,
                    "compiled functions refer to an undiscovered parent",
                    "compile functions only from the closed discovery graph",
                ));
                continue;
            };
            for function in compiled {
                let Some(authored) = declaration
                    .functions
                    .iter()
                    .find(|candidate| candidate.name == function.rust_name)
                else {
                    diagnostics.push(diag(
                        ModuleDiagnosticCode::AuthoringFingerprintMismatch,
                        Some(function.metadata.source.clone()),
                        "a compiled function has no matching authored bridge",
                        "recompile the source and procedural authoring surface together",
                    ));
                    continue;
                };
                let rust_symbol =
                    RustSymbol::new(format!("{}::{}", parent.as_str(), function.rust_name))
                        .map_err(|_| {
                            singleton(diag(
                                ModuleDiagnosticCode::DescriptorInvalid,
                                Some(function.metadata.source.clone()),
                                "a compiled function symbol is not canonical",
                                "use one ordinary Rust identifier for the exported function",
                            ))
                        })?;
                functions.push(FunctionDescriptor {
                    parent: parent.clone(),
                    rust_symbol,
                    wire_name: function.wire_name.clone(),
                    compiled: function.clone(),
                    fingerprint: authored.fingerprint.clone(),
                    source: authored.source.clone(),
                });
            }
        }
        functions.sort_by(|left, right| {
            local_names[&left.parent]
                .cmp(&local_names[&right.parent])
                .then_with(|| left.wire_name.cmp(&right.wire_name))
                .then_with(|| left.rust_symbol.cmp(&right.rust_symbol))
        });

        let mut seen_dispatch = BTreeSet::new();
        let mut dispatch = Vec::new();
        for function in &functions {
            let coordinate = DispatchCoordinate {
                parent: local_names[&function.parent].clone(),
                function: function.wire_name.clone(),
            };
            if !seen_dispatch.insert(coordinate.clone()) {
                diagnostics.push(diag(
                    ModuleDiagnosticCode::DispatchDuplicate,
                    Some(function.source.clone()),
                    "a callable dispatch coordinate occurs more than once",
                    "retain one function for each parent and function wire-name pair",
                ));
            } else {
                dispatch.push(coordinate);
            }
        }

        if !diagnostics.is_empty() {
            return Err(ModuleDiagnosticSet::new(diagnostics)
                .expect("descriptor construction collected at least one diagnostic"));
        }

        let source_files = input
            .discovery
            .visited_documents
            .iter()
            .filter_map(|path| {
                input
                    .source
                    .documents
                    .get(path)
                    .map(|document| (path.clone(), document.digest.clone()))
            })
            .collect();
        let module = WireName::new(input.source.package.name.as_str().to_case(Case::Pascal))
            .map_err(|_| {
                singleton(diag(
                    ModuleDiagnosticCode::NameInvalid,
                    None,
                    "the package name cannot form a module wire name",
                    "choose a package name that normalizes to one target identifier",
                ))
            })?;
        let root = input.discovery.root.clone();
        let placeholder = Sha256Digest::hash_bytes(b"pending module descriptor digest");
        let provenance = DescriptorProvenance {
            source_files,
            cfg: input.source.cfg.clone(),
            visible_schema_digest: input.target.visible_schema_digest.clone(),
            generator_digest: input.generator_digest.clone(),
            authoring_abi: AuthoringAbi::current(),
        };
        let mut descriptor = ModuleDescriptor {
            format_version: super::model::FormatVersion::current(),
            authoring_abi: AuthoringAbi::current(),
            target: input.target.clone(),
            package: input.source.package.clone(),
            module,
            root,
            types,
            functions,
            dispatch,
            source_digest: input.source.digest.clone(),
            generator_digest: input.generator_digest.clone(),
            provenance,
            digest: placeholder,
        };
        descriptor.digest = descriptor_digest(&descriptor).map_err(|_| {
            singleton(diag(
                ModuleDiagnosticCode::DescriptorInvalid,
                None,
                "the canonical descriptor could not be encoded",
                "repair the typed descriptor input before projection",
            ))
        })?;
        Ok(descriptor)
    }
}

/// Recomputes descriptor identity while excluding the identity field itself.
pub fn descriptor_digest(
    descriptor: &ModuleDescriptor,
) -> Result<Sha256Digest, super::canonical::CanonicalError> {
    let mut value =
        serde_json::to_value(descriptor).map_err(super::canonical::CanonicalError::Encode)?;
    if let Value::Object(object) = &mut value {
        object.remove("digest");
    }
    canonical_digest(DigestDomain::ModuleDescriptor, &value)
}

fn local_wire_names(
    discovery: &ModuleDiscovery,
) -> Result<BTreeMap<RustSymbol, WireName>, ModuleDiagnosticSet> {
    let mut names = BTreeMap::new();
    let mut owners = BTreeMap::<WireName, RustSymbol>::new();
    let mut diagnostics = Vec::new();
    for declaration in discovery.declarations.values() {
        let value = declaration
            .metadata
            .get("rename")
            .map(|value| {
                syn::parse_str::<syn::LitStr>(value)
                    .map(|literal| literal.value())
                    .map_err(|_| ())
            })
            .transpose()
            .map_err(|_| {
                singleton(diag(
                    ModuleDiagnosticCode::MetadataMalformed,
                    Some(declaration.source.clone()),
                    "a type rename is not a Rust string literal",
                    "use one quoted target identifier",
                ))
            })?
            .unwrap_or_else(|| {
                final_segment(declaration.rust_symbol.as_str()).to_case(Case::Pascal)
            });
        let wire_name = WireName::new(value).map_err(|_| {
            singleton(diag(
                ModuleDiagnosticCode::NameInvalid,
                Some(declaration.source.clone()),
                "an exported type wire name is invalid",
                "use one valid target identifier",
            ))
        })?;
        if owners
            .insert(wire_name.clone(), declaration.rust_symbol.clone())
            .is_some()
        {
            diagnostics.push(diag(
                ModuleDiagnosticCode::WireNameCollision,
                Some(declaration.source.clone()),
                "multiple exported types normalize to one wire name",
                "give each exported type a unique explicit rename",
            ));
        }
        names.insert(declaration.rust_symbol.clone(), wire_name);
    }
    if diagnostics.is_empty() {
        Ok(names)
    } else {
        Err(ModuleDiagnosticSet::new(diagnostics)
            .expect("type-name compilation collected at least one diagnostic"))
    }
}

fn type_descriptor(
    declaration: &AuthoringDeclaration,
    local_names: &BTreeMap<RustSymbol, WireName>,
    discovery: &ModuleDiscovery,
    catalog: &TypeCatalog,
) -> Result<LocalTypeDescriptor, ModuleDiagnostic> {
    let (kind, contract) = match declaration.kind {
        AuthoringDeclarationKind::Object => {
            let contract = catalog
                .objects
                .get(&declaration.rust_symbol)
                .ok_or_else(|| {
                    diag(
                        ModuleDiagnosticCode::DescriptorInvalid,
                        Some(declaration.source.clone()),
                        "an object lacks its compiled state contract",
                        "compile the complete type catalog before descriptor construction",
                    )
                })?;
            (
                LocalTypeKind::Object {
                    root: declaration.rust_symbol == discovery.root,
                },
                LocalTypeContract::Object(contract.clone()),
            )
        }
        AuthoringDeclarationKind::Interface => {
            let contract = catalog
                .interfaces
                .get(&declaration.rust_symbol)
                .ok_or_else(|| {
                    diag(
                        ModuleDiagnosticCode::DescriptorInvalid,
                        Some(declaration.source.clone()),
                        "an interface lacks its compiled contract",
                        "compile the complete type catalog before descriptor construction",
                    )
                })?;
            (
                LocalTypeKind::Interface,
                LocalTypeContract::Interface(contract.clone()),
            )
        }
        AuthoringDeclarationKind::Enum => {
            let contract = catalog.enums.get(&declaration.rust_symbol).ok_or_else(|| {
                diag(
                    ModuleDiagnosticCode::DescriptorInvalid,
                    Some(declaration.source.clone()),
                    "an enum lacks its compiled contract",
                    "compile the complete type catalog before descriptor construction",
                )
            })?;
            (
                LocalTypeKind::Enum,
                LocalTypeContract::Enum(contract.clone()),
            )
        }
        AuthoringDeclarationKind::Scalar => {
            let contract = catalog
                .scalars
                .get(&declaration.rust_symbol)
                .ok_or_else(|| {
                    diag(
                        ModuleDiagnosticCode::DescriptorInvalid,
                        Some(declaration.source.clone()),
                        "a scalar lacks its compiled contract",
                        "compile the complete type catalog before descriptor construction",
                    )
                })?;
            (
                LocalTypeKind::Scalar {
                    representation: RustSymbol::new(format!(
                        "crate::{}",
                        rust_type_name(&contract.representation)
                    ))
                    .unwrap_or_else(|_| declaration.rust_symbol.clone()),
                },
                LocalTypeContract::Scalar(contract.clone()),
            )
        }
    };

    let fields = match &contract {
        LocalTypeContract::Object(object) => object
            .fields
            .iter()
            .filter_map(|field| {
                declaration
                    .fields
                    .iter()
                    .find(|authored| authored.name == field.rust_name)
                    .map(|authored| FieldDescriptor {
                        rust_name: field.rust_name.clone(),
                        wire_name: field.wire_name.clone(),
                        ty: field.ty.clone(),
                        mode: field.mode,
                        documentation: authored.documentation.clone(),
                        deprecation: authored.deprecation.clone(),
                        source: authored.source.clone(),
                    })
            })
            .collect(),
        _ => Vec::new(),
    };
    let enum_values = match &contract {
        LocalTypeContract::Enum(enum_contract) => {
            let members = enum_contract.wire_members()?;
            declaration
                .variants
                .iter()
                .filter_map(|variant| {
                    members
                        .get(&variant.name)
                        .map(|wire_name| EnumValueDescriptor {
                            rust_name: variant.name.clone(),
                            wire_name: wire_name.clone(),
                            documentation: variant.documentation.clone(),
                            deprecation: variant.deprecation.clone(),
                            source: variant.source.clone(),
                        })
                })
                .collect()
        }
        _ => Vec::new(),
    };
    let interface_functions = if declaration.kind == AuthoringDeclarationKind::Interface {
        interface_functions(declaration, discovery, local_names)?
    } else {
        Vec::new()
    };

    Ok(LocalTypeDescriptor {
        rust_symbol: declaration.rust_symbol.clone(),
        wire_name: local_names[&declaration.rust_symbol].clone(),
        kind,
        contract,
        fields,
        interface_functions,
        enum_values,
        documentation: declaration.documentation.clone(),
        deprecation: declaration.deprecation.clone(),
        source: declaration.source.clone(),
        fingerprint: declaration.fingerprint.clone(),
    })
}

fn interface_functions(
    declaration: &AuthoringDeclaration,
    discovery: &ModuleDiscovery,
    local_names: &BTreeMap<RustSymbol, WireName>,
) -> Result<Vec<ProjectedFunction>, ModuleDiagnostic> {
    let resolver = TypeResolver::new(discovery);
    let mut functions = Vec::new();
    for method in &declaration.interface_methods {
        let mut arguments = Vec::new();
        for parameter in &method.parameters {
            let ty = resolver.resolve(
                &parameter.rust_type,
                TypePosition::Input,
                Some(parameter.source.clone()),
            )?;
            arguments.push(ProjectedArgument {
                wire_name: metadata_wire_name(
                    parameter.metadata.get("rename"),
                    &parameter.name,
                    &parameter.source,
                )?,
                ty: project_type(&ty, local_names),
                optional: matches!(ty, RustModuleType::Optional(_)),
                default: None,
                default_path: None,
                default_address: None,
                ignore: Vec::new(),
                documentation: parameter.documentation.clone(),
                deprecation: parameter.deprecation.clone(),
                source: parameter.source.clone(),
            });
        }
        let output = method.output.trim().trim_start_matches("->").trim();
        let ty = if output.is_empty() {
            RustModuleType::Void
        } else {
            let parsed = syn::parse_str::<Type>(output).map_err(|_| {
                diag(
                    ModuleDiagnosticCode::InterfaceInvalid,
                    Some(method.source.clone()),
                    "an interface result type could not be parsed",
                    "use one concrete supported result type",
                )
            })?;
            let success = result_success(&parsed).unwrap_or(&parsed);
            resolver.resolve(
                &success.to_token_stream().to_string(),
                TypePosition::Output,
                Some(method.source.clone()),
            )?
        };
        functions.push(ProjectedFunction {
            wire_name: metadata_wire_name(
                method.metadata.get("rename"),
                &method.name,
                &method.source,
            )?,
            arguments,
            return_type: project_type(&ty, local_names),
            constructor: false,
            cache: super::metadata::CachePolicy::Default,
            role: super::metadata::FunctionRole::Ordinary,
            documentation: method.documentation.clone(),
            deprecation: method.deprecation.clone(),
            source: method.source.clone(),
        });
    }
    functions.sort_by(|left, right| left.wire_name.cmp(&right.wire_name));
    Ok(functions)
}

/// Projects a closed Rust type using descriptor-owned local wire names.
pub(crate) fn project_type(
    ty: &RustModuleType,
    local_names: &BTreeMap<RustSymbol, WireName>,
) -> ProjectedType {
    match ty {
        RustModuleType::String => named("String", false),
        RustModuleType::Integer => named("Integer", false),
        RustModuleType::Boolean => named("Boolean", false),
        RustModuleType::Float => named("Float", false),
        RustModuleType::Void => ProjectedType::Void,
        RustModuleType::List(element) => ProjectedType::List {
            element: Box::new(project_type(element, local_names)),
            nullable: false,
        },
        RustModuleType::Optional(inner) => match project_type(inner, local_names) {
            ProjectedType::Named { name, .. } => ProjectedType::Named {
                name,
                nullable: true,
            },
            ProjectedType::List { element, .. } => ProjectedType::List {
                element,
                nullable: true,
            },
            ProjectedType::Void => ProjectedType::Void,
        },
        RustModuleType::LocalObject(symbol)
        | RustModuleType::LocalInterface(symbol)
        | RustModuleType::LocalEnum(symbol)
        | RustModuleType::CustomScalar(symbol) => named(local_names[symbol].as_str(), false),
        RustModuleType::GeneratedObject(name) | RustModuleType::GeneratedInterface(name) => {
            named(name, false)
        }
    }
}

fn named(name: &str, nullable: bool) -> ProjectedType {
    ProjectedType::Named {
        name: name.to_owned(),
        nullable,
    }
}

fn metadata_wire_name(
    explicit: Option<&String>,
    rust_name: &str,
    source: &super::model::SourceCoordinate,
) -> Result<WireName, ModuleDiagnostic> {
    let value = explicit
        .map(|value| syn::parse_str::<syn::LitStr>(value).map(|literal| literal.value()))
        .transpose()
        .map_err(|_| {
            diag(
                ModuleDiagnosticCode::MetadataMalformed,
                Some(source.clone()),
                "a wire rename is not a Rust string literal",
                "use one quoted target identifier",
            )
        })?
        .unwrap_or_else(|| rust_name.to_case(Case::Camel));
    WireName::new(value).map_err(|_| {
        diag(
            ModuleDiagnosticCode::NameInvalid,
            Some(source.clone()),
            "a normalized wire name is invalid",
            "use one valid target identifier",
        )
    })
}

fn result_success(ty: &Type) -> Option<&Type> {
    let Type::Path(path) = ty else {
        return None;
    };
    let segment = path.path.segments.last()?;
    if segment.ident != "Result" {
        return None;
    }
    let PathArguments::AngleBracketed(arguments) = &segment.arguments else {
        return None;
    };
    arguments.args.iter().find_map(|argument| match argument {
        GenericArgument::Type(ty) => Some(ty),
        _ => None,
    })
}

fn rust_type_name(ty: &RustModuleType) -> &'static str {
    match ty {
        RustModuleType::String => "String",
        RustModuleType::Integer => "i64",
        RustModuleType::Boolean => "bool",
        RustModuleType::Float => "f64",
        _ => "Unsupported",
    }
}

fn final_segment(symbol: &str) -> &str {
    symbol.rsplit("::").next().unwrap_or(symbol)
}

fn diag(
    code: ModuleDiagnosticCode,
    source: Option<super::model::SourceCoordinate>,
    message: &'static str,
    remediation: &'static str,
) -> ModuleDiagnostic {
    ModuleDiagnostic::new(code, source, message, remediation)
        .expect("static descriptor diagnostics satisfy the safe renderer policy")
}

fn singleton(diagnostic: ModuleDiagnostic) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new(vec![diagnostic])
        .expect("a singleton descriptor diagnostic set is non-empty")
}
