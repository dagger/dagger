//! Descriptor-only registration and introspection projection.
//!
//! Both views are assembled from the same typed rows and compared before either is
//! returned. Source parsing, metadata inference, and target name selection are already
//! complete, so projection cannot silently reinterpret the authored contract.

use std::collections::{BTreeMap, BTreeSet};

use super::descriptor::project_type;
use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::metadata::FunctionKind;
use super::model::{
    FunctionDescriptor, LocalTypeContract, LocalTypeDescriptor, LocalTypeKind, ModuleDescriptor,
    ModuleIntrospection, ProjectedArgument, ProjectedEnumValue, ProjectedField, ProjectedFunction,
    ProjectedTypeDef, ProjectedTypeKind, RegistrationProjection, RustSymbol, WireName,
};
use super::types::ObjectFieldMode;

/// Pair of all-or-nothing descriptor projections.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleProjections {
    /// Ordered engine registration values.
    pub registration: RegistrationProjection,
    /// Structurally equivalent module introspection.
    pub introspection: ModuleIntrospection,
}

/// Pure descriptor projection compiler.
pub struct ProjectionCompiler;

impl ProjectionCompiler {
    /// Derives equivalent registration and introspection or returns neither.
    pub fn project(
        descriptor: &ModuleDescriptor,
        visible_type_names: &BTreeSet<WireName>,
    ) -> Result<ModuleProjections, ModuleDiagnosticSet> {
        let mut diagnostics = Vec::new();
        for local in &descriptor.types {
            if visible_type_names.contains(&local.wire_name) {
                diagnostics.push(diag(
                    ModuleDiagnosticCode::VisibleSchemaCollision,
                    Some(local.source.clone()),
                    "a local type conflicts with the checked visible schema",
                    "rename the local type or repair the selected dependency namespace",
                ));
            }
        }
        if !diagnostics.is_empty() {
            return Err(ModuleDiagnosticSet::new(diagnostics)
                .expect("schema collision projection collected diagnostics"));
        }

        let local_names = descriptor
            .types
            .iter()
            .map(|ty| (ty.rust_symbol.clone(), ty.wire_name.clone()))
            .collect::<BTreeMap<_, _>>();
        let mut types = BTreeMap::new();
        for local in &descriptor.types {
            let projected = project_local_type(descriptor, local, &local_names)?;
            types.insert(local.wire_name.clone(), projected);
        }

        let constructors = descriptor
            .functions
            .iter()
            .filter(|function| function.compiled.kind == FunctionKind::Constructor)
            .collect::<Vec<_>>();
        let [constructor] = constructors.as_slice() else {
            return Err(singleton(diag(
                ModuleDiagnosticCode::ProjectionMismatch,
                None,
                "the descriptor does not contain exactly one root constructor",
                "retain exactly one descriptor-owned constructor on the module root",
            )));
        };
        let query_name = WireName::new("Query").expect("Query is a valid target identifier");
        let query = ProjectedTypeDef {
            wire_name: query_name.clone(),
            kind: ProjectedTypeKind::Object,
            fields: Vec::new(),
            functions: vec![project_function(constructor, &local_names)],
            enum_values: Vec::new(),
            interfaces: Vec::new(),
            documentation: Some("The module query root.".to_owned()),
            deprecation: None,
            source: constructor.source.clone(),
        };
        types.insert(query_name, query);

        let registration = RegistrationProjection {
            format_version: descriptor.format_version,
            descriptor_digest: descriptor.digest.clone(),
            types: types.clone(),
        };
        let introspection = ModuleIntrospection {
            format_version: descriptor.format_version,
            descriptor_digest: descriptor.digest.clone(),
            types,
        };
        if registration.types != introspection.types {
            return Err(singleton(diag(
                ModuleDiagnosticCode::ProjectionMismatch,
                None,
                "registration and introspection differ structurally",
                "derive both projections from the same descriptor rows",
            )));
        }
        Ok(ModuleProjections {
            registration,
            introspection,
        })
    }
}

fn project_local_type(
    descriptor: &ModuleDescriptor,
    local: &LocalTypeDescriptor,
    local_names: &BTreeMap<RustSymbol, WireName>,
) -> Result<ProjectedTypeDef, ModuleDiagnosticSet> {
    let kind = match local.kind {
        LocalTypeKind::Object { .. } => ProjectedTypeKind::Object,
        LocalTypeKind::Interface => ProjectedTypeKind::Interface,
        LocalTypeKind::Enum => ProjectedTypeKind::Enum,
        LocalTypeKind::Scalar { .. } => ProjectedTypeKind::Scalar,
    };
    let fields = local
        .fields
        .iter()
        .filter(|field| field.mode == ObjectFieldMode::Exposed)
        .map(|field| ProjectedField {
            wire_name: field.wire_name.clone(),
            ty: project_type(&field.ty, local_names),
            documentation: field.documentation.clone(),
            deprecation: field.deprecation.clone(),
            source: field.source.clone(),
        })
        .collect();
    let mut functions = descriptor
        .functions
        .iter()
        .filter(|function| {
            function.parent == local.rust_symbol
                && function.compiled.kind != FunctionKind::Constructor
        })
        .map(|function| project_function(function, local_names))
        .collect::<Vec<_>>();
    functions.extend(local.interface_functions.clone());
    functions.sort_by(|left, right| left.wire_name.cmp(&right.wire_name));

    let interfaces = descriptor
        .types
        .iter()
        .filter_map(|candidate| match &candidate.contract {
            LocalTypeContract::Interface(contract)
                if contract.implementations.contains(&local.rust_symbol) =>
            {
                Some(candidate.wire_name.clone())
            }
            _ => None,
        })
        .collect();

    Ok(ProjectedTypeDef {
        wire_name: local.wire_name.clone(),
        kind,
        fields,
        functions,
        enum_values: local
            .enum_values
            .iter()
            .map(|variant| ProjectedEnumValue {
                wire_name: variant.wire_name.clone(),
                documentation: variant.documentation.clone(),
                deprecation: variant.deprecation.clone(),
                source: variant.source.clone(),
            })
            .collect(),
        interfaces,
        documentation: local.documentation.clone(),
        deprecation: local.deprecation.clone(),
        source: local.source.clone(),
    })
}

fn project_function(
    function: &FunctionDescriptor,
    local_names: &BTreeMap<RustSymbol, WireName>,
) -> ProjectedFunction {
    ProjectedFunction {
        wire_name: function.wire_name.clone(),
        arguments: function
            .compiled
            .arguments
            .iter()
            .map(|argument| ProjectedArgument {
                wire_name: argument.wire_name.clone(),
                ty: project_type(&argument.ty, local_names),
                optional: argument.metadata.optional,
                default: argument.metadata.default.clone(),
                default_path: argument.metadata.default_path.clone(),
                default_address: argument.metadata.default_address.clone(),
                ignore: argument.metadata.ignore.clone(),
                documentation: argument.metadata.documentation.clone(),
                deprecation: argument.metadata.deprecation.clone(),
                source: argument.metadata.source.clone(),
            })
            .collect(),
        return_type: project_type(function.compiled.return_type.success(), local_names),
        constructor: function.compiled.kind == FunctionKind::Constructor,
        cache: function.compiled.metadata.cache,
        role: function.compiled.metadata.role,
        documentation: function.compiled.metadata.documentation.clone(),
        deprecation: function.compiled.metadata.deprecation.clone(),
        source: function.source.clone(),
    }
}

fn diag(
    code: ModuleDiagnosticCode,
    source: Option<super::model::SourceCoordinate>,
    message: &'static str,
    remediation: &'static str,
) -> ModuleDiagnostic {
    ModuleDiagnostic::new(code, source, message, remediation)
        .expect("static projection diagnostics satisfy the safe renderer policy")
}

fn singleton(diagnostic: ModuleDiagnostic) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new(vec![diagnostic])
        .expect("a singleton projection diagnostic set is non-empty")
}
