//! Deterministic, engine-free discovery over immutable Rust source snapshots.
//!
//! Filesystem traversal belongs to the engine-side snapshot builder. This module sees
//! only canonical UTF-8 documents, follows Rust module declarations within that closed
//! set, evaluates the explicitly supplied cfg environment, resolves ordinary local
//! paths, and computes the unique exported type closure without executing code.

use std::collections::{BTreeMap, BTreeSet, VecDeque};
use std::num::NonZeroU32;

use quote::ToTokens;
use serde::Serialize;
use syn::parse::Parser;
use syn::spanned::Spanned;
use syn::{Attribute, Item, Meta, Type, UseTree};

use super::authoring::{
    AuthoringDeclaration, AuthoringDeclarationKind, AuthoringFieldPolicy, AuthoringFunction,
    AuthoringParser,
};
use super::canonical::{DigestDomain, canonical_digest};
use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::model::{
    ModuleSourcePath, ModuleSourceSnapshot, RustSymbol, Sha256Digest, SourceCoordinate,
};

const MAX_MODULE_DEPTH: usize = 64;

/// Target kind retained for a checked generated type reference.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum GeneratedTypeKind {
    /// Generated object handle.
    Object,
    /// Generated interface handle.
    Interface,
}

/// One checked generated type visible to source discovery.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct GeneratedTypeBinding {
    /// Rust path accepted in authored signatures, including any dependency alias.
    pub rust_path: String,
    /// Canonical target type identity.
    pub target_name: String,
    /// Checked visible-schema provenance.
    pub visible_schema_digest: Sha256Digest,
    /// Target kind used by the closed type algebra.
    pub kind: GeneratedTypeKind,
}

/// Closed generated-type index for one exact visible schema.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct GeneratedTypeRegistry {
    expected_digest: Sha256Digest,
    bindings: BTreeMap<String, GeneratedTypeBinding>,
}

impl GeneratedTypeRegistry {
    /// Builds an exact-target registry and rejects duplicate Rust paths.
    pub fn new(
        expected_digest: Sha256Digest,
        bindings: impl IntoIterator<Item = GeneratedTypeBinding>,
    ) -> Result<Self, String> {
        let mut indexed = BTreeMap::new();
        for binding in bindings {
            if binding.rust_path.is_empty()
                || binding.target_name.is_empty()
                || indexed.insert(binding.rust_path.clone(), binding).is_some()
            {
                return Err("generated type bindings require unique non-empty paths".to_owned());
            }
        }
        Ok(Self {
            expected_digest,
            bindings: indexed,
        })
    }

    /// Returns an empty registry for a schema with no visible generated types.
    #[must_use]
    pub fn empty(expected_digest: Sha256Digest) -> Self {
        Self {
            expected_digest,
            bindings: BTreeMap::new(),
        }
    }

    /// Borrows the exact visible-schema digest required by every binding.
    #[must_use]
    pub const fn expected_digest(&self) -> &Sha256Digest {
        &self.expected_digest
    }
}

/// One generated reference admitted into the local authoring closure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResolvedGeneratedType {
    /// Authored Rust spelling after import resolution.
    pub rust_path: String,
    /// Target identity reused without local redefinition.
    pub target_name: String,
    /// Object or interface kind.
    pub kind: GeneratedTypeKind,
}

/// One terminating source type alias retained for closed type resolution.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResolvedTypeAlias {
    /// Canonical crate-relative alias path.
    pub rust_path: String,
    /// Ordered generic parameter names.
    pub parameters: Vec<String>,
    /// Authored target type syntax.
    pub target: String,
}

/// Canonical result of pure module discovery.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleDiscovery {
    /// The one explicit root object.
    pub root: RustSymbol,
    /// Exported local closure keyed by canonical Rust symbol.
    pub declarations: BTreeMap<RustSymbol, AuthoringDeclaration>,
    /// Explicit local interface implementations after path and method-shape validation.
    pub interface_implementations: BTreeMap<RustSymbol, BTreeSet<RustSymbol>>,
    /// Terminating source aliases keyed by canonical crate-relative path.
    pub type_aliases: BTreeMap<String, ResolvedTypeAlias>,
    /// Checked generated references reached by the local closure.
    pub generated_types: BTreeMap<String, ResolvedGeneratedType>,
    /// External source documents reached through valid module declarations.
    pub visited_documents: BTreeSet<ModuleSourcePath>,
    /// Identity of the immutable source snapshot consumed by discovery.
    pub source_digest: Sha256Digest,
}

/// Pure compiler phase for Rust module and transitive type discovery.
pub struct SourceDiscovery;

impl SourceDiscovery {
    /// Discovers one exact root and the complete supported local type closure.
    pub fn discover(
        snapshot: &ModuleSourceSnapshot,
        generated: &GeneratedTypeRegistry,
    ) -> Result<ModuleDiscovery, ModuleDiagnosticSet> {
        let mut diagnostics = validate_snapshot(snapshot);
        let mut units = Vec::new();
        let mut visited = BTreeSet::new();
        let mut active = BTreeSet::new();

        walk_document(
            snapshot,
            &snapshot.package.crate_root,
            Vec::new(),
            source_parent(snapshot.package.crate_root.as_str()),
            0,
            &mut units,
            &mut visited,
            &mut active,
            &mut diagnostics,
        );

        let mut declarations = BTreeMap::new();
        let mut detached_functions = Vec::new();
        let mut pending_implementations = Vec::new();
        let mut scopes = BTreeMap::new();
        for unit in &units {
            match AuthoringParser::parse_in_module_configured(
                &unit.path,
                &unit.contents,
                &unit.module_path,
                &snapshot.cfg,
            ) {
                Ok(parsed) => {
                    for declaration in parsed {
                        let symbol = declaration.rust_symbol.clone();
                        if declarations.insert(symbol.clone(), declaration).is_some() {
                            diagnostics.push(diag(
                                ModuleDiagnosticCode::RustPathInvalid,
                                None,
                                "a crate-local Rust symbol was declared more than once",
                                "keep one canonical declaration for each exported symbol",
                            ));
                        }
                    }
                }
                Err(errors) => diagnostics.extend(errors.diagnostics().iter().cloned()),
            }
            match AuthoringParser::functions_in_module_configured(
                &unit.path,
                &unit.contents,
                &unit.module_path,
                &snapshot.cfg,
            ) {
                Ok(functions) => detached_functions.extend(functions),
                Err(errors) => diagnostics.extend(errors.diagnostics().iter().cloned()),
            }
            if let Ok(file) =
                AuthoringParser::configured_file(&unit.path, &unit.contents, &snapshot.cfg)
            {
                collect_scopes(
                    &unit.path,
                    &file.items,
                    &unit.module_path,
                    &mut scopes,
                    &mut diagnostics,
                );
                collect_interface_implementations(
                    &unit.path,
                    &file.items,
                    &unit.module_path,
                    &mut pending_implementations,
                    &mut diagnostics,
                );
            }
        }

        merge_detached_functions(&mut declarations, detached_functions, &mut diagnostics);
        validate_aliases(&scopes, &mut diagnostics);
        let type_aliases = resolved_aliases(&scopes);
        let interface_implementations = resolve_interface_implementations(
            pending_implementations,
            &scopes,
            &declarations,
            &mut diagnostics,
        );

        let roots = declarations
            .values()
            .filter(|declaration| {
                declaration.kind == AuthoringDeclarationKind::Object
                    && declaration.metadata.contains_key("root")
            })
            .map(|declaration| declaration.rust_symbol.clone())
            .collect::<Vec<_>>();
        let root = match roots.as_slice() {
            [root] => Some(root.clone()),
            [] => {
                diagnostics.push(diag(
                    ModuleDiagnosticCode::RootMissing,
                    None,
                    "no explicit Dagger root object was discovered",
                    "mark exactly one crate-accessible object as the module root",
                ));
                None
            }
            _ => {
                diagnostics.push(diag(
                    ModuleDiagnosticCode::RootAmbiguous,
                    declarations
                        .get(&roots[1])
                        .map(|declaration| declaration.source.clone()),
                    "more than one explicit Dagger root object was discovered",
                    "retain exactly one root marker in the selected package",
                ));
                None
            }
        };

        let mut reached = declarations.keys().cloned().collect::<BTreeSet<_>>();
        if let Some(root) = &root {
            reached.insert(root.clone());
        }
        let mut queue = reached.iter().cloned().collect::<VecDeque<_>>();
        let mut resolved_generated = BTreeMap::new();
        while let Some(symbol) = queue.pop_front() {
            let Some(declaration) = declarations.get(&symbol) else {
                continue;
            };
            let module = symbol_module(&symbol);
            for reference in declaration_references(declaration) {
                resolve_reference(
                    &reference,
                    &module,
                    declaration.source.clone(),
                    &scopes,
                    &declarations,
                    generated,
                    &mut reached,
                    &mut queue,
                    &mut resolved_generated,
                    &mut diagnostics,
                );
            }
        }

        if let Some(errors) = ModuleDiagnosticSet::new(diagnostics) {
            return Err(errors);
        }
        let Some(root) = root else {
            return Err(single(diag(
                ModuleDiagnosticCode::RootMissing,
                None,
                "no explicit Dagger root object was discovered",
                "mark exactly one crate-accessible object as the module root",
            )));
        };
        declarations.retain(|symbol, _| reached.contains(symbol));
        Ok(ModuleDiscovery {
            root,
            declarations,
            interface_implementations,
            type_aliases,
            generated_types: resolved_generated,
            visited_documents: visited,
            source_digest: snapshot.digest.clone(),
        })
    }
}

#[derive(Clone)]
struct SourceUnit {
    path: ModuleSourcePath,
    contents: String,
    module_path: Vec<String>,
}

// Traversal state stays explicit so each mutable collection has one clear owner and
// recursive calls cannot retain an opaque context with broader mutation authority.
#[allow(clippy::too_many_arguments)]
fn walk_document(
    snapshot: &ModuleSourceSnapshot,
    path: &ModuleSourcePath,
    module_path: Vec<String>,
    module_directory: String,
    depth: usize,
    units: &mut Vec<SourceUnit>,
    visited: &mut BTreeSet<ModuleSourcePath>,
    active: &mut BTreeSet<ModuleSourcePath>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    if depth > MAX_MODULE_DEPTH {
        diagnostics.push(diag(
            ModuleDiagnosticCode::SourceModuleInvalid,
            source_start(path),
            "Rust module traversal exceeded its depth bound",
            "remove a recursive or excessively deep module chain",
        ));
        return;
    }
    if !active.insert(path.clone()) {
        diagnostics.push(diag(
            ModuleDiagnosticCode::SourceModuleInvalid,
            source_start(path),
            "Rust module declarations form a cycle",
            "remove the recursive module path",
        ));
        return;
    }
    if !visited.insert(path.clone()) {
        active.remove(path);
        return;
    }
    let Some(document) = snapshot.documents.get(path) else {
        diagnostics.push(diag(
            ModuleDiagnosticCode::SourceModuleInvalid,
            source_start(path),
            "a declared Rust module document is absent from the snapshot",
            "add the confined module source or repair the module declaration",
        ));
        active.remove(path);
        return;
    };
    units.push(SourceUnit {
        path: path.clone(),
        contents: document.contents.clone(),
        module_path: module_path.clone(),
    });

    let file = match syn::parse_file(&document.contents) {
        Ok(file) => file,
        Err(error) => {
            diagnostics.push(diag(
                ModuleDiagnosticCode::SourceModuleInvalid,
                Some(coordinate(path, error.span())),
                "Rust source could not be parsed for module traversal",
                "repair the authored Rust syntax",
            ));
            active.remove(path);
            return;
        }
    };
    walk_items(
        snapshot,
        path,
        &file.items,
        &module_path,
        &module_directory,
        depth,
        units,
        visited,
        active,
        diagnostics,
    );
    active.remove(path);
}

#[allow(clippy::too_many_arguments)]
fn walk_items(
    snapshot: &ModuleSourceSnapshot,
    containing_path: &ModuleSourcePath,
    items: &[Item],
    module_path: &[String],
    module_directory: &str,
    depth: usize,
    units: &mut Vec<SourceUnit>,
    visited: &mut BTreeSet<ModuleSourcePath>,
    active: &mut BTreeSet<ModuleSourcePath>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    for item in items {
        let Item::Mod(module) = item else {
            continue;
        };
        match cfg_enabled(&module.attrs, &snapshot.cfg) {
            Ok(false) => continue,
            Ok(true) => {}
            Err(()) => {
                diagnostics.push(diag(
                    ModuleDiagnosticCode::CfgUnresolved,
                    Some(coordinate(containing_path, module.span())),
                    "a module depends on configuration absent from the snapshot",
                    "declare the custom cfg input explicitly or remove the dependency",
                ));
                continue;
            }
        }
        let mut child_module = module_path.to_vec();
        child_module.push(module.ident.to_string());
        if let Some((_, nested)) = &module.content {
            let child_directory = join_relative(module_directory, &module.ident.to_string());
            walk_items(
                snapshot,
                containing_path,
                nested,
                &child_module,
                &child_directory,
                depth + 1,
                units,
                visited,
                active,
                diagnostics,
            );
            continue;
        }

        let explicit = path_attribute(&module.attrs);
        let candidates = if let Some(explicit) = explicit {
            vec![join_relative(module_directory, &explicit)]
        } else {
            let name = module.ident.to_string();
            vec![
                join_relative(module_directory, &format!("{name}.rs")),
                join_relative(module_directory, &format!("{name}/mod.rs")),
            ]
        };
        let existing = candidates
            .into_iter()
            .filter_map(|candidate| ModuleSourcePath::new(candidate).ok())
            .filter(|candidate| snapshot.documents.contains_key(candidate))
            .collect::<Vec<_>>();
        let [child] = existing.as_slice() else {
            diagnostics.push(diag(
                ModuleDiagnosticCode::SourceModuleInvalid,
                Some(coordinate(containing_path, module.ident.span())),
                if existing.is_empty() {
                    "a declared Rust module has no confined source document"
                } else {
                    "a declared Rust module resolves to more than one source document"
                },
                "retain one canonical in-snapshot module path",
            ));
            continue;
        };
        let child_directory = child_module_directory(child.as_str());
        walk_document(
            snapshot,
            child,
            child_module,
            child_directory,
            depth + 1,
            units,
            visited,
            active,
            diagnostics,
        );
    }
}

#[derive(Clone, Default)]
struct Scope {
    imports: BTreeMap<String, BTreeSet<String>>,
    globs: BTreeSet<String>,
    aliases: BTreeMap<String, Alias>,
}

#[derive(Clone)]
struct Alias {
    parameters: Vec<String>,
    target: Type,
    source: SourceCoordinate,
}

struct PendingInterfaceImplementation {
    module: String,
    interface: String,
    object: String,
    methods: BTreeMap<String, String>,
    source: SourceCoordinate,
}

fn collect_interface_implementations(
    path: &ModuleSourcePath,
    items: &[Item],
    module_path: &[String],
    implementations: &mut Vec<PendingInterfaceImplementation>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    for item in items {
        match item {
            Item::Impl(item_impl)
                if item_impl.attrs.iter().any(|attribute| {
                    attribute
                        .path()
                        .segments
                        .last()
                        .is_some_and(|segment| segment.ident == "methods")
                }) && item_impl.trait_.is_some() =>
            {
                let Some((negation, trait_path, _)) = &item_impl.trait_ else {
                    continue;
                };
                let Type::Path(object) = item_impl.self_ty.as_ref() else {
                    diagnostics.push(diag(
                        ModuleDiagnosticCode::InterfaceInvalid,
                        Some(coordinate(path, item_impl.self_ty.span())),
                        "an interface implementation object path is unsupported",
                        "implement the interface for one concrete local object",
                    ));
                    continue;
                };
                if negation.is_some()
                    || object.qself.is_some()
                    || !item_impl.generics.params.is_empty()
                    || item_impl.generics.where_clause.is_some()
                {
                    diagnostics.push(diag(
                        ModuleDiagnosticCode::InterfaceInvalid,
                        Some(coordinate(path, item_impl.impl_token.span)),
                        "an interface implementation uses unsupported negation or generics",
                        "use one positive concrete local trait implementation",
                    ));
                    continue;
                }
                let mut methods = BTreeMap::new();
                let mut invalid = false;
                for impl_item in &item_impl.items {
                    let syn::ImplItem::Fn(function) = impl_item else {
                        invalid = true;
                        break;
                    };
                    let shape = signature_shape(&function.sig);
                    if methods
                        .insert(function.sig.ident.to_string(), shape)
                        .is_some()
                    {
                        invalid = true;
                        break;
                    }
                }
                if invalid {
                    diagnostics.push(diag(
                        ModuleDiagnosticCode::InterfaceInvalid,
                        Some(coordinate(path, item_impl.impl_token.span)),
                        "an interface implementation has unsupported or duplicate members",
                        "implement each declared interface method exactly once",
                    ));
                    continue;
                }
                implementations.push(PendingInterfaceImplementation {
                    module: module_key(module_path),
                    interface: path_spelling(trait_path),
                    object: path_spelling(&object.path),
                    methods,
                    source: coordinate(path, item_impl.impl_token.span),
                });
            }
            Item::Mod(module) => {
                if let Some((_, nested)) = &module.content {
                    let mut child = module_path.to_vec();
                    child.push(module.ident.to_string());
                    collect_interface_implementations(
                        path,
                        nested,
                        &child,
                        implementations,
                        diagnostics,
                    );
                }
            }
            _ => {}
        }
    }
}

fn resolve_interface_implementations(
    pending: Vec<PendingInterfaceImplementation>,
    scopes: &BTreeMap<String, Scope>,
    declarations: &BTreeMap<RustSymbol, AuthoringDeclaration>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) -> BTreeMap<RustSymbol, BTreeSet<RustSymbol>> {
    let mut resolved = BTreeMap::<RustSymbol, BTreeSet<RustSymbol>>::new();
    for implementation in pending {
        let interface = resolve_declared_symbol(
            &implementation.interface,
            &implementation.module,
            scopes.get(&implementation.module),
            declarations,
        );
        let object = resolve_declared_symbol(
            &implementation.object,
            &implementation.module,
            scopes.get(&implementation.module),
            declarations,
        );
        let (Some(interface), Some(object)) = (interface, object) else {
            diagnostics.push(diag(
                ModuleDiagnosticCode::InterfaceInvalid,
                Some(implementation.source),
                "an interface implementation path is unresolved or ambiguous",
                "qualify one exported local interface and object",
            ));
            continue;
        };
        let interface_declaration = &declarations[&interface];
        if interface_declaration.kind != AuthoringDeclarationKind::Interface
            || declarations[&object].kind != AuthoringDeclarationKind::Object
        {
            diagnostics.push(diag(
                ModuleDiagnosticCode::InterfaceInvalid,
                Some(implementation.source),
                "an implementation does not connect an exported interface and object",
                "annotate one local trait implementation for one local object",
            ));
            continue;
        }
        let expected = interface_declaration
            .interface_methods
            .iter()
            .map(|method| (method.name.clone(), authoring_method_shape(method)))
            .collect::<BTreeMap<_, _>>();
        if expected != implementation.methods {
            diagnostics.push(diag(
                ModuleDiagnosticCode::InterfaceInvalid,
                Some(implementation.source),
                "an interface implementation method shape is incomplete or incompatible",
                "implement every interface method with the exact declared Rust type shape",
            ));
            continue;
        }
        resolved.entry(interface).or_default().insert(object);
    }
    resolved
}

fn resolve_declared_symbol(
    reference: &str,
    module: &str,
    scope: Option<&Scope>,
    declarations: &BTreeMap<RustSymbol, AuthoringDeclaration>,
) -> Option<RustSymbol> {
    let candidates = path_candidates(reference, module, scope)
        .into_iter()
        .filter_map(|candidate| RustSymbol::new(candidate).ok())
        .filter(|candidate| declarations.contains_key(candidate))
        .collect::<BTreeSet<_>>();
    if let [symbol] = candidates.iter().cloned().collect::<Vec<_>>().as_slice() {
        return Some(symbol.clone());
    }
    unique_symbol_by_name(declarations.keys(), final_segment(reference))
}

fn authoring_method_shape(method: &super::authoring::AuthoringInterfaceMethod) -> String {
    let inputs = method
        .parameters
        .iter()
        .map(|parameter| normalized_type_spelling(&parameter.rust_type))
        .collect::<Vec<_>>()
        .join(",");
    format!(
        "&self({inputs})->{}",
        normalized_type_spelling(method.output.trim_start_matches("->").trim())
    )
}

fn signature_shape(signature: &syn::Signature) -> String {
    let receiver = if signature
        .receiver()
        .is_some_and(|receiver| receiver.reference.is_some() && receiver.mutability.is_none())
    {
        "&self"
    } else {
        "unsupported"
    };
    let inputs = signature
        .inputs
        .iter()
        .filter_map(|input| match input {
            syn::FnArg::Receiver(_) => None,
            syn::FnArg::Typed(argument) => Some(normalized_type_spelling(
                &argument.ty.to_token_stream().to_string(),
            )),
        })
        .collect::<Vec<_>>()
        .join(",");
    let output = match &signature.output {
        syn::ReturnType::Default => String::new(),
        syn::ReturnType::Type(_, ty) => normalized_type_spelling(&ty.to_token_stream().to_string()),
    };
    format!("{receiver}({inputs})->{output}")
}

fn normalized_type_spelling(spelling: &str) -> String {
    syn::parse_str::<Type>(spelling)
        .map(|ty| ty.to_token_stream().to_string().replace(' ', ""))
        .unwrap_or_else(|_| spelling.replace(' ', ""))
}

fn path_spelling(path: &syn::Path) -> String {
    path.segments
        .iter()
        .map(|segment| segment.ident.to_string())
        .collect::<Vec<_>>()
        .join("::")
}

fn collect_scopes(
    path: &ModuleSourcePath,
    items: &[Item],
    module_path: &[String],
    scopes: &mut BTreeMap<String, Scope>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    let key = module_key(module_path);
    for item in items {
        match item {
            Item::Use(item_use) => {
                let mut leaves = Vec::new();
                flatten_use(&item_use.tree, Vec::new(), &mut leaves);
                let scope = scopes.entry(key.clone()).or_default();
                for leaf in leaves {
                    match leaf {
                        UseLeaf::Named {
                            alias,
                            path: import,
                        } => {
                            scope.imports.entry(alias).or_default().insert(import);
                        }
                        UseLeaf::Glob(import) => {
                            scope.globs.insert(import);
                        }
                    }
                }
            }
            Item::Type(alias) => {
                let scope = scopes.entry(key.clone()).or_default();
                let parameters = alias
                    .generics
                    .type_params()
                    .map(|parameter| parameter.ident.to_string())
                    .collect();
                if scope
                    .aliases
                    .insert(
                        alias.ident.to_string(),
                        Alias {
                            parameters,
                            target: (*alias.ty).clone(),
                            source: coordinate(path, alias.ident.span()),
                        },
                    )
                    .is_some()
                {
                    diagnostics.push(diag(
                        ModuleDiagnosticCode::RustPathInvalid,
                        Some(coordinate(path, alias.ident.span())),
                        "a type alias is declared more than once in one module",
                        "retain one unambiguous type alias",
                    ));
                }
            }
            Item::Mod(module) => {
                if let Some((_, nested)) = &module.content {
                    let mut child = module_path.to_vec();
                    child.push(module.ident.to_string());
                    collect_scopes(path, nested, &child, scopes, diagnostics);
                }
            }
            _ => {}
        }
    }
}

enum UseLeaf {
    Named { alias: String, path: String },
    Glob(String),
}

fn flatten_use(tree: &UseTree, mut prefix: Vec<String>, leaves: &mut Vec<UseLeaf>) {
    match tree {
        UseTree::Path(path) => {
            prefix.push(path.ident.to_string());
            flatten_use(&path.tree, prefix, leaves);
        }
        UseTree::Name(name) => {
            prefix.push(name.ident.to_string());
            leaves.push(UseLeaf::Named {
                alias: name.ident.to_string(),
                path: prefix.join("::"),
            });
        }
        UseTree::Rename(rename) => {
            prefix.push(rename.ident.to_string());
            leaves.push(UseLeaf::Named {
                alias: rename.rename.to_string(),
                path: prefix.join("::"),
            });
        }
        UseTree::Glob(_) => leaves.push(UseLeaf::Glob(prefix.join("::"))),
        UseTree::Group(group) => {
            for item in &group.items {
                flatten_use(item, prefix.clone(), leaves);
            }
        }
    }
}

fn merge_detached_functions(
    declarations: &mut BTreeMap<RustSymbol, AuthoringDeclaration>,
    functions: Vec<(RustSymbol, AuthoringFunction)>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    for (suggested_parent, function) in functions {
        let parent = if declarations.contains_key(&suggested_parent) {
            Some(suggested_parent)
        } else {
            unique_symbol_by_name(
                declarations.keys(),
                final_segment(suggested_parent.as_str()),
            )
        };
        let Some(parent) = parent else {
            diagnostics.push(diag(
                ModuleDiagnosticCode::RustPathInvalid,
                Some(function.source.clone()),
                "an exported impl parent is unresolved or ambiguous",
                "import or qualify one exported local object",
            ));
            continue;
        };
        let declaration = declarations
            .get_mut(&parent)
            .expect("the resolved parent is present in the declaration map");
        if !declaration.functions.iter().any(|existing| {
            existing.name == function.name
                && existing.source == function.source
                && existing.fingerprint == function.fingerprint
        }) {
            declaration.functions.push(function);
            declaration.functions.sort_by(|left, right| {
                left.name
                    .cmp(&right.name)
                    .then_with(|| left.source.cmp(&right.source))
            });
        }
    }
}

fn declaration_references(declaration: &AuthoringDeclaration) -> Vec<String> {
    let mut references = Vec::new();
    for field in &declaration.fields {
        if field.policy != AuthoringFieldPolicy::Transient {
            references.extend(type_paths(&field.rust_type));
        }
    }
    for function in &declaration.functions {
        for parameter in &function.parameters {
            if !parameter.metadata.contains_key("context") {
                references.extend(type_paths(&parameter.rust_type));
            }
        }
        references.extend(type_paths(function.output.trim_start_matches("->").trim()));
    }
    for method in &declaration.interface_methods {
        for parameter in &method.parameters {
            references.extend(type_paths(&parameter.rust_type));
        }
        references.extend(type_paths(method.output.trim_start_matches("->").trim()));
    }
    references.sort();
    references.dedup();
    references
}

#[allow(clippy::too_many_arguments)]
fn resolve_reference(
    reference: &str,
    module: &str,
    source: SourceCoordinate,
    scopes: &BTreeMap<String, Scope>,
    declarations: &BTreeMap<RustSymbol, AuthoringDeclaration>,
    generated: &GeneratedTypeRegistry,
    reached: &mut BTreeSet<RustSymbol>,
    queue: &mut VecDeque<RustSymbol>,
    resolved_generated: &mut BTreeMap<String, ResolvedGeneratedType>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    if is_builtin(reference) {
        return;
    }
    if let Some(scope) = scopes.get(module)
        && let Some(alias) = scope.aliases.get(reference)
    {
        for target in type_paths(&alias.target.to_token_stream().to_string()) {
            if !alias
                .parameters
                .iter()
                .any(|parameter| parameter == final_segment(&target))
            {
                resolve_reference(
                    &target,
                    module,
                    alias.source.clone(),
                    scopes,
                    declarations,
                    generated,
                    reached,
                    queue,
                    resolved_generated,
                    diagnostics,
                );
            }
        }
        return;
    }

    let candidates = path_candidates(reference, module, scopes.get(module));
    let local = candidates
        .iter()
        .filter_map(|candidate| RustSymbol::new(candidate.clone()).ok())
        .filter(|candidate| declarations.contains_key(candidate))
        .collect::<BTreeSet<_>>();
    let local = if local.is_empty() {
        unique_symbol_by_name(declarations.keys(), final_segment(reference))
            .into_iter()
            .collect()
    } else {
        local
    };
    if local.len() == 1 {
        let symbol = local
            .into_iter()
            .next()
            .expect("a singleton resolution set contains one symbol");
        if reached.insert(symbol.clone()) {
            queue.push_back(symbol);
        }
        return;
    }
    if local.len() > 1 {
        diagnostics.push(diag(
            ModuleDiagnosticCode::RustPathInvalid,
            Some(source),
            "a referenced local Rust path is ambiguous",
            "qualify the type or replace ambiguous glob imports",
        ));
        return;
    }

    let generated_matches = generated
        .bindings
        .values()
        .filter(|binding| {
            candidates
                .iter()
                .any(|candidate| candidate == &binding.rust_path)
                || final_segment(&binding.rust_path) == final_segment(reference)
        })
        .collect::<Vec<_>>();
    match generated_matches.as_slice() {
        [binding] if binding.visible_schema_digest == generated.expected_digest => {
            resolved_generated.insert(
                binding.rust_path.clone(),
                ResolvedGeneratedType {
                    rust_path: binding.rust_path.clone(),
                    target_name: binding.target_name.clone(),
                    kind: binding.kind,
                },
            );
        }
        [..] if generated_matches
            .iter()
            .any(|binding| binding.visible_schema_digest != generated.expected_digest) =>
        {
            diagnostics.push(diag(
                ModuleDiagnosticCode::GeneratedTypeStale,
                Some(source),
                "a generated type has incompatible visible-schema provenance",
                "regenerate the checked binding for the exact target",
            ))
        }
        [] => diagnostics.push(diag(
            ModuleDiagnosticCode::ForeignTypeUnsupported,
            Some(source),
            "an exported contract references an unsupported foreign Rust type",
            "use the closed module type policy or a checked generated binding",
        )),
        _ => diagnostics.push(diag(
            ModuleDiagnosticCode::RustPathInvalid,
            Some(source),
            "a generated Rust type path is ambiguous",
            "qualify the checked generated type",
        )),
    }
}

fn validate_aliases(scopes: &BTreeMap<String, Scope>, diagnostics: &mut Vec<ModuleDiagnostic>) {
    for scope in scopes.values() {
        for name in scope.aliases.keys() {
            let mut active = BTreeSet::new();
            if alias_cycles(name, scope, &mut active) {
                let alias = &scope.aliases[name];
                diagnostics.push(diag(
                    ModuleDiagnosticCode::RustPathInvalid,
                    Some(alias.source.clone()),
                    "a type alias expands recursively",
                    "make the alias terminate in the supported type algebra",
                ));
            }
        }
        for imports in scope.imports.values() {
            if imports.len() > 1 {
                diagnostics.push(diag(
                    ModuleDiagnosticCode::RustPathInvalid,
                    None,
                    "an imported Rust name has more than one source",
                    "rename or qualify the ambiguous imports",
                ));
            }
        }
    }
}

fn resolved_aliases(scopes: &BTreeMap<String, Scope>) -> BTreeMap<String, ResolvedTypeAlias> {
    scopes
        .iter()
        .flat_map(|(module, scope)| {
            scope.aliases.iter().map(move |(name, alias)| {
                let rust_path = format!("{module}::{name}");
                (
                    rust_path.clone(),
                    ResolvedTypeAlias {
                        rust_path,
                        parameters: alias.parameters.clone(),
                        target: alias.target.to_token_stream().to_string(),
                    },
                )
            })
        })
        .collect()
}

fn alias_cycles(name: &str, scope: &Scope, active: &mut BTreeSet<String>) -> bool {
    if !active.insert(name.to_owned()) {
        return true;
    }
    let cycles = scope.aliases.get(name).is_some_and(|alias| {
        type_paths(&alias.target.to_token_stream().to_string())
            .into_iter()
            .filter(|candidate| scope.aliases.contains_key(final_segment(candidate)))
            .any(|candidate| alias_cycles(final_segment(&candidate), scope, active))
    });
    active.remove(name);
    cycles
}

fn path_candidates(reference: &str, module: &str, scope: Option<&Scope>) -> BTreeSet<String> {
    let mut candidates = BTreeSet::new();
    if reference.starts_with("crate::") {
        candidates.insert(reference.to_owned());
        return candidates;
    }
    if let Some(rest) = reference.strip_prefix("self::") {
        candidates.insert(format!("{module}::{rest}"));
        return candidates;
    }
    if let Some(rest) = reference.strip_prefix("super::") {
        let parent = module
            .rsplit_once("::")
            .map_or("crate", |(parent, _)| parent);
        candidates.insert(format!("{parent}::{rest}"));
        return candidates;
    }
    let first = reference.split("::").next().unwrap_or(reference);
    let rest = reference.strip_prefix(first).unwrap_or_default();
    if let Some(scope) = scope {
        if let Some(imports) = scope.imports.get(first) {
            for import in imports {
                candidates.insert(normalize_crate_path(&format!("{import}{rest}"), module));
            }
        }
        for glob in &scope.globs {
            candidates.insert(normalize_crate_path(
                &format!("{glob}::{reference}"),
                module,
            ));
        }
    }
    candidates.insert(format!("{module}::{reference}"));
    candidates
}

fn normalize_crate_path(path: &str, module: &str) -> String {
    if path.starts_with("crate::") {
        path.to_owned()
    } else if let Some(rest) = path.strip_prefix("self::") {
        format!("{module}::{rest}")
    } else if let Some(rest) = path.strip_prefix("super::") {
        let parent = module
            .rsplit_once("::")
            .map_or("crate", |(parent, _)| parent);
        format!("{parent}::{rest}")
    } else {
        format!("crate::{path}")
    }
}

fn type_paths(source: &str) -> Vec<String> {
    let Ok(parsed) = syn::parse_str::<Type>(source) else {
        return Vec::new();
    };
    let mut paths = Vec::new();
    collect_type_paths(&parsed, &mut paths);
    paths
}

fn collect_type_paths(ty: &Type, paths: &mut Vec<String>) {
    match ty {
        Type::Path(path) => {
            let spelling = path
                .path
                .segments
                .iter()
                .map(|segment| segment.ident.to_string())
                .collect::<Vec<_>>()
                .join("::");
            if !spelling.is_empty() {
                paths.push(spelling);
            }
            for segment in &path.path.segments {
                if let syn::PathArguments::AngleBracketed(arguments) = &segment.arguments {
                    for argument in &arguments.args {
                        if let syn::GenericArgument::Type(argument) = argument {
                            collect_type_paths(argument, paths);
                        }
                    }
                }
            }
        }
        Type::Reference(reference) => collect_type_paths(&reference.elem, paths),
        Type::Tuple(tuple) => {
            for element in &tuple.elems {
                collect_type_paths(element, paths);
            }
        }
        Type::Array(array) => collect_type_paths(&array.elem, paths),
        Type::Slice(slice) => collect_type_paths(&slice.elem, paths),
        Type::Paren(paren) => collect_type_paths(&paren.elem, paths),
        Type::Group(group) => collect_type_paths(&group.elem, paths),
        Type::Ptr(pointer) => collect_type_paths(&pointer.elem, paths),
        _ => {}
    }
}

pub(super) fn cfg_enabled(
    attributes: &[Attribute],
    environment: &super::model::CfgEnvironment,
) -> Result<bool, ()> {
    for attribute in attributes {
        if attribute.path().is_ident("cfg") {
            let meta = attribute.parse_args::<Meta>().map_err(|_| ())?;
            if !eval_cfg(&meta, environment)? {
                return Ok(false);
            }
        }
    }
    Ok(true)
}

fn eval_cfg(meta: &Meta, environment: &super::model::CfgEnvironment) -> Result<bool, ()> {
    match meta {
        Meta::Path(path) => {
            let key = path.get_ident().map(ToString::to_string).ok_or(())?;
            environment
                .values
                .get(&key)
                .map(|values| values.is_empty() || values.contains("true"))
                .ok_or(())
        }
        Meta::NameValue(value) => {
            let key = value.path.get_ident().map(ToString::to_string).ok_or(())?;
            let syn::Expr::Lit(expression) = &value.value else {
                return Err(());
            };
            let syn::Lit::Str(value) = &expression.lit else {
                return Err(());
            };
            if key == "feature" {
                Ok(environment.features.contains(&value.value()))
            } else {
                environment
                    .values
                    .get(&key)
                    .map(|values| values.contains(&value.value()))
                    .ok_or(())
            }
        }
        Meta::List(list) => {
            let operator = list.path.get_ident().map(ToString::to_string).ok_or(())?;
            let parser = syn::punctuated::Punctuated::<Meta, syn::Token![,]>::parse_terminated;
            let nested = parser.parse2(list.tokens.clone()).map_err(|_| ())?;
            match operator.as_str() {
                "all" => nested.iter().try_fold(true, |result, item| {
                    Ok(result && eval_cfg(item, environment)?)
                }),
                "any" => nested.iter().try_fold(false, |result, item| {
                    Ok(result || eval_cfg(item, environment)?)
                }),
                "not" if nested.len() == 1 => Ok(!eval_cfg(&nested[0], environment)?),
                _ => Err(()),
            }
        }
    }
}

#[derive(Serialize)]
struct SnapshotIdentity<'a> {
    format_version: &'a super::model::FormatVersion,
    package: &'a super::model::ModulePackage,
    cfg: &'a super::model::CfgEnvironment,
    documents: &'a BTreeMap<ModuleSourcePath, super::model::SourceDocument>,
}

/// Computes the canonical identity of a snapshot without recursively including it.
pub fn source_snapshot_digest(
    snapshot: &ModuleSourceSnapshot,
) -> Result<Sha256Digest, super::canonical::CanonicalError> {
    canonical_digest(
        DigestDomain::SourceSnapshot,
        &SnapshotIdentity {
            format_version: &snapshot.format_version,
            package: &snapshot.package,
            cfg: &snapshot.cfg,
            documents: &snapshot.documents,
        },
    )
}

fn validate_snapshot(snapshot: &ModuleSourceSnapshot) -> Vec<ModuleDiagnostic> {
    let mut diagnostics = Vec::new();
    if !snapshot
        .documents
        .contains_key(&snapshot.package.crate_root)
    {
        diagnostics.push(diag(
            ModuleDiagnosticCode::SourceModuleInvalid,
            source_start(&snapshot.package.crate_root),
            "the selected crate root is absent from the source snapshot",
            "include the exact selected crate root",
        ));
    }
    for (path, document) in &snapshot.documents {
        if path != &document.path
            || document.digest != Sha256Digest::hash_bytes(document.contents.as_bytes())
        {
            diagnostics.push(diag(
                ModuleDiagnosticCode::SourceModuleInvalid,
                source_start(path),
                "a source document path or digest is inconsistent",
                "rebuild the immutable source snapshot",
            ));
        }
    }
    match source_snapshot_digest(snapshot) {
        Ok(digest) if digest == snapshot.digest => {}
        _ => diagnostics.push(diag(
            ModuleDiagnosticCode::SourceModuleInvalid,
            None,
            "the source snapshot digest is stale",
            "rebuild the immutable source snapshot from confined inputs",
        )),
    }
    diagnostics
}

fn path_attribute(attributes: &[Attribute]) -> Option<String> {
    attributes.iter().find_map(|attribute| {
        if !attribute.path().is_ident("path") {
            return None;
        }
        let Meta::NameValue(value) = &attribute.meta else {
            return None;
        };
        let syn::Expr::Lit(expression) = &value.value else {
            return None;
        };
        let syn::Lit::Str(path) = &expression.lit else {
            return None;
        };
        Some(path.value())
    })
}

fn is_builtin(path: &str) -> bool {
    matches!(
        final_segment(path),
        "String" | "i64" | "bool" | "f64" | "Vec" | "Option" | "Result" | "ModuleContext" | "Self"
    )
}

fn unique_symbol_by_name<'a>(
    symbols: impl IntoIterator<Item = &'a RustSymbol>,
    name: &str,
) -> Option<RustSymbol> {
    let mut matches = symbols
        .into_iter()
        .filter(|symbol| final_segment(symbol.as_str()) == name);
    let first = matches.next()?.clone();
    matches.next().is_none().then_some(first)
}

fn final_segment(path: &str) -> &str {
    path.rsplit("::").next().unwrap_or(path)
}

fn symbol_module(symbol: &RustSymbol) -> String {
    symbol
        .as_str()
        .rsplit_once("::")
        .map_or_else(|| "crate".to_owned(), |(module, _)| module.to_owned())
}

fn module_key(module_path: &[String]) -> String {
    std::iter::once("crate")
        .chain(module_path.iter().map(String::as_str))
        .collect::<Vec<_>>()
        .join("::")
}

fn source_parent(path: &str) -> String {
    path.rsplit_once('/')
        .map_or_else(String::new, |(parent, _)| parent.to_owned())
}

fn child_module_directory(path: &str) -> String {
    if let Some(prefix) = path.strip_suffix("/mod.rs") {
        prefix.to_owned()
    } else if let Some(prefix) = path.strip_suffix(".rs") {
        prefix.to_owned()
    } else {
        source_parent(path)
    }
}

fn join_relative(parent: &str, child: &str) -> String {
    if parent.is_empty() {
        child.to_owned()
    } else {
        format!("{parent}/{child}")
    }
}

fn source_start(path: &ModuleSourcePath) -> Option<SourceCoordinate> {
    Some(SourceCoordinate {
        path: path.clone(),
        line: NonZeroU32::MIN,
        column: NonZeroU32::MIN,
    })
}

fn coordinate(path: &ModuleSourcePath, span: proc_macro2::Span) -> SourceCoordinate {
    let start = span.start();
    SourceCoordinate {
        path: path.clone(),
        line: NonZeroU32::new(u32::try_from(start.line).unwrap_or(u32::MAX).max(1))
            .expect("source lines are clamped to non-zero"),
        column: NonZeroU32::new(
            u32::try_from(start.column.saturating_add(1))
                .unwrap_or(u32::MAX)
                .max(1),
        )
        .expect("source columns are clamped to non-zero"),
    }
}

fn diag(
    code: ModuleDiagnosticCode,
    source: Option<SourceCoordinate>,
    message: &'static str,
    remediation: &'static str,
) -> ModuleDiagnostic {
    ModuleDiagnostic::new(code, source, message, remediation)
        .expect("reviewed source diagnostics satisfy the safe renderer policy")
}

fn single(diagnostic: ModuleDiagnostic) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new([diagnostic]).expect("one diagnostic forms a non-empty diagnostic set")
}

#[cfg(test)]
mod tests {
    use std::collections::{BTreeMap, BTreeSet};

    use proptest::prelude::*;

    use super::*;
    use crate::module::model::{
        CfgEnvironment, FormatVersion, ModulePackage, PackageName, SourceDocument, TargetValue,
    };

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(256))]

        // Discovery is invariant under snapshot insertion order and closes transitive local references.
        #[test]
        fn property_04_source_discovery_closed_deterministic_inert(
            reverse in any::<bool>(),
            nest_inline in any::<bool>(),
            seed in any::<u16>(),
        ) {
            let root_source = if nest_inline {
                format!(r#"
                    mod nested {{
                        #[dagger_sdk::object]
                        pub(crate) struct Child{seed} {{ #[dagger(field)] value: String }}
                    }}
                    #[dagger_sdk::object(root)]
                    pub struct Root{seed} {{ #[dagger(field)] child: nested::Child{seed} }}
                "#)
            } else {
                format!(r#"
                    mod nested;
                    #[dagger_sdk::object(root)]
                    pub struct Root{seed} {{ #[dagger(field)] child: nested::Child{seed} }}
                "#)
            };
            let child_source = format!(r#"
                #[dagger_sdk::object]
                pub(crate) struct Child{seed} {{ #[dagger(field)] value: String }}
            "#);
            let mut files = vec![("src/lib.rs", root_source)];
            if !nest_inline {
                files.push(("src/nested.rs", child_source));
            }
            if reverse {
                files.reverse();
            }
            let snapshot = snapshot(files);
            let registry = GeneratedTypeRegistry::empty(snapshot.package_digest());
            let discovered = SourceDiscovery::discover(&snapshot, &registry).unwrap();
            prop_assert_eq!(discovered.declarations.len(), 2);
            prop_assert_eq!(discovered.root.as_str(), format!("crate::Root{seed}"));
            let child = format!("crate::nested::Child{seed}");
            prop_assert!(discovered.declarations.keys().any(|symbol| symbol.as_str() == child));
        }
    }

    #[test]
    fn unresolved_cfg_and_escaping_path_are_typed_failures() {
        let snapshot = snapshot(vec![(
            "src/lib.rs",
            r#"
                #[cfg(build_script_answer)]
                mod hidden;
                #[path = "../escape.rs"]
                mod escape;
                #[dagger_sdk::object(root)]
                pub struct Root { #[dagger(field)] value: String }
            "#
            .to_owned(),
        )]);
        let errors = SourceDiscovery::discover(
            &snapshot,
            &GeneratedTypeRegistry::empty(snapshot.package_digest()),
        )
        .unwrap_err();
        let codes = errors
            .diagnostics()
            .iter()
            .map(ModuleDiagnostic::code)
            .collect::<BTreeSet<_>>();
        assert!(codes.contains(&ModuleDiagnosticCode::CfgUnresolved));
        assert!(codes.contains(&ModuleDiagnosticCode::SourceModuleInvalid));
    }

    #[test]
    fn configured_declarations_and_explicit_interface_implementations_are_exact() {
        let snapshot = snapshot(vec![(
            "src/lib.rs",
            r#"
                #[cfg(feature = "disabled")]
                #[dagger_sdk::object(root)]
                pub struct DisabledRoot {}

                #[dagger_sdk::object(root, default = true)]
                pub struct Root {}

                #[dagger_sdk::interface]
                pub trait Named {
                    fn name(&self) -> String;
                }

                #[dagger_sdk::object]
                pub struct Child {}

                #[dagger_sdk::methods]
                impl Named for Child {
                    fn name(&self) -> String { "child".to_owned() }
                }
            "#
            .to_owned(),
        )]);
        let discovered = SourceDiscovery::discover(
            &snapshot,
            &GeneratedTypeRegistry::empty(snapshot.package_digest()),
        )
        .unwrap();
        assert_eq!(discovered.root.as_str(), "crate::Root");
        assert!(
            !discovered
                .declarations
                .keys()
                .any(|symbol| symbol.as_str() == "crate::DisabledRoot")
        );
        assert_eq!(
            discovered
                .interface_implementations
                .get(&RustSymbol::new("crate::Named").unwrap()),
            Some(&BTreeSet::from([RustSymbol::new("crate::Child").unwrap()]))
        );
    }

    fn snapshot(files: Vec<(&str, String)>) -> ModuleSourceSnapshot {
        let mut documents = BTreeMap::new();
        for (path, contents) in files {
            let path = ModuleSourcePath::new(path).expect("fixture path is canonical");
            documents.insert(path.clone(), SourceDocument::new(path, contents));
        }
        let crate_root = ModuleSourcePath::new("src/lib.rs").expect("fixture root is canonical");
        let mut snapshot = ModuleSourceSnapshot {
            format_version: FormatVersion::current(),
            package: ModulePackage {
                name: PackageName::new("fixture").expect("fixture package is valid"),
                crate_root,
                edition: TargetValue::new("2024").expect("fixture edition is valid"),
            },
            cfg: CfgEnvironment {
                values: BTreeMap::from([("unix".to_owned(), BTreeSet::new())]),
                features: BTreeSet::new(),
            },
            documents,
            digest: Sha256Digest::hash_bytes(b"pending"),
        };
        snapshot.digest = source_snapshot_digest(&snapshot).expect("fixture snapshot hashes");
        snapshot
    }

    trait SnapshotTestExt {
        fn package_digest(&self) -> Sha256Digest;
    }

    impl SnapshotTestExt for ModuleSourceSnapshot {
        fn package_digest(&self) -> Sha256Digest {
            Sha256Digest::hash_bytes(self.package.name.as_str().as_bytes())
        }
    }
}
