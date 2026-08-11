//! Closed Rust-to-Dagger type policy and engine-independent value codecs.
//!
//! Every admitted Rust shape has one recursive target representation and one typed
//! value path. Unsupported wrappers, integer families, references, generics, and JSON
//! kinds fail explicitly; no branch erases an unknown value into untyped JSON.

use std::collections::{BTreeMap, BTreeSet};

use convert_case::{Case, Casing};
use quote::ToTokens;
use serde_json::{Map, Number, Value};
use syn::{GenericArgument, PathArguments, Type};

use super::authoring::{AuthoringDeclaration, AuthoringDeclarationKind};
use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::model::{RustSymbol, SourceCoordinate, WireName};
use super::source::{GeneratedTypeKind, ModuleDiscovery};

/// Position-specific validation for the recursive Rust type algebra.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TypePosition {
    /// Function or constructor data argument.
    Input,
    /// Function or constructor successful result.
    Output,
    /// Persistent local object state.
    State,
}

/// One lossless Rust type admitted by module authoring.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum RustModuleType {
    /// Owned UTF-8 string.
    String,
    /// Target signed integer.
    Integer,
    /// Boolean scalar.
    Boolean,
    /// Finite target float represented by a JSON number.
    Float,
    /// Successful unit result, projected as target Void.
    Void,
    /// Ordered recursive list.
    List(Box<RustModuleType>),
    /// One representable omission/nullability layer.
    Optional(Box<RustModuleType>),
    /// Local object with descriptor-owned state.
    LocalObject(RustSymbol),
    /// Local closed interface.
    LocalInterface(RustSymbol),
    /// Local unit enum.
    LocalEnum(RustSymbol),
    /// Local transparent scalar.
    CustomScalar(RustSymbol),
    /// Checked generated object handle.
    GeneratedObject(String),
    /// Checked generated interface handle.
    GeneratedInterface(String),
}

/// Canonical target TypeDef shape derived from the Rust algebra.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum ProjectedType {
    /// Named scalar, object, interface, or enum.
    Named { name: String, nullable: bool },
    /// Recursive ordered list.
    List {
        element: Box<ProjectedType>,
        nullable: bool,
    },
    /// Target Void.
    Void,
}

impl RustModuleType {
    /// Projects the exact recursive target wrapper shape.
    #[must_use]
    pub fn projected(&self) -> ProjectedType {
        match self {
            Self::String => named("String", false),
            Self::Integer => named("Integer", false),
            Self::Boolean => named("Boolean", false),
            Self::Float => named("Float", false),
            Self::Void => ProjectedType::Void,
            Self::List(element) => ProjectedType::List {
                element: Box::new(element.projected()),
                nullable: false,
            },
            Self::Optional(inner) => make_nullable(inner.projected()),
            Self::LocalObject(symbol)
            | Self::LocalInterface(symbol)
            | Self::LocalEnum(symbol)
            | Self::CustomScalar(symbol) => named(final_segment(symbol.as_str()), false),
            Self::GeneratedObject(name) | Self::GeneratedInterface(name) => named(name, false),
        }
    }
}

fn named(name: &str, nullable: bool) -> ProjectedType {
    ProjectedType::Named {
        name: name.to_owned(),
        nullable,
    }
}

fn make_nullable(projected: ProjectedType) -> ProjectedType {
    match projected {
        ProjectedType::Named { name, .. } => ProjectedType::Named {
            name,
            nullable: true,
        },
        ProjectedType::List { element, .. } => ProjectedType::List {
            element,
            nullable: true,
        },
        ProjectedType::Void => ProjectedType::Void,
    }
}

/// Resolves authored Rust type syntax against one completed discovery graph.
pub struct TypeResolver<'a> {
    discovery: &'a ModuleDiscovery,
}

impl<'a> TypeResolver<'a> {
    /// Creates a resolver whose local/generated names cannot expand after validation.
    #[must_use]
    pub const fn new(discovery: &'a ModuleDiscovery) -> Self {
        Self { discovery }
    }

    /// Parses and validates one authored type at its semantic position.
    pub fn resolve(
        &self,
        spelling: &str,
        position: TypePosition,
        source: Option<SourceCoordinate>,
    ) -> Result<RustModuleType, ModuleDiagnostic> {
        let ty = syn::parse_str::<Type>(spelling).map_err(|_| {
            diagnostic(
                ModuleDiagnosticCode::TypeUnsupported,
                source.clone(),
                "an authored Rust type could not be parsed",
                "use one concrete type from the closed module type policy",
            )
        })?;
        self.resolve_type(&ty, position, source)
    }

    fn resolve_type(
        &self,
        ty: &Type,
        position: TypePosition,
        source: Option<SourceCoordinate>,
    ) -> Result<RustModuleType, ModuleDiagnostic> {
        match ty {
            Type::Tuple(tuple) if tuple.elems.is_empty() && position == TypePosition::Output => {
                Ok(RustModuleType::Void)
            }
            Type::Path(path) if path.qself.is_none() => {
                let segment =
                    path.path.segments.last().ok_or_else(|| {
                        unsupported(source.clone(), "an authored Rust path is empty")
                    })?;
                let name = segment.ident.to_string();
                match name.as_str() {
                    "String" if no_arguments(&segment.arguments) => Ok(RustModuleType::String),
                    "i64" if no_arguments(&segment.arguments) => Ok(RustModuleType::Integer),
                    "bool" if no_arguments(&segment.arguments) => Ok(RustModuleType::Boolean),
                    "f64" if no_arguments(&segment.arguments) => Ok(RustModuleType::Float),
                    "Vec" => {
                        let inner = one_type_argument(&segment.arguments).ok_or_else(|| {
                            unsupported(source.clone(), "Vec requires one concrete element type")
                        })?;
                        Ok(RustModuleType::List(Box::new(
                            self.resolve_type(inner, position, source)?,
                        )))
                    }
                    "Option" => {
                        let inner = one_type_argument(&segment.arguments).ok_or_else(|| {
                            unsupported(source.clone(), "Option requires one concrete inner type")
                        })?;
                        let inner = self.resolve_type(inner, position, source.clone())?;
                        if matches!(inner, RustModuleType::Optional(_) | RustModuleType::Void) {
                            return Err(unsupported(
                                source,
                                "the nested optional shape loses a Rust distinction in the target",
                            ));
                        }
                        Ok(RustModuleType::Optional(Box::new(inner)))
                    }
                    "Result" => Err(unsupported(
                        source,
                        "Result is a function outcome and not a data type",
                    )),
                    "i8" | "i16" | "i32" | "i128" | "isize" | "u8" | "u16" | "u32" | "u64"
                    | "u128" | "usize" | "f32" => Err(unsupported(
                        source,
                        "this numeric Rust type has no lossless target mapping",
                    )),
                    _ => {
                        if let Some(expanded) = self.expand_alias(path, source.clone())? {
                            self.resolve_type(&expanded, position, source)
                        } else {
                            self.resolve_named(&path.path.to_token_stream().to_string(), source)
                        }
                    }
                }
            }
            Type::Paren(paren) => self.resolve_type(&paren.elem, position, source),
            Type::Group(group) => self.resolve_type(&group.elem, position, source),
            Type::Reference(_) | Type::Ptr(_) => Err(unsupported(
                source,
                "borrowed and pointer types cannot cross the module boundary",
            )),
            Type::Tuple(_)
            | Type::Array(_)
            | Type::Slice(_)
            | Type::ImplTrait(_)
            | Type::TraitObject(_)
            | Type::BareFn(_)
            | Type::Infer(_)
            | Type::Macro(_)
            | Type::Never(_)
            | Type::Verbatim(_) => Err(unsupported(
                source,
                "the authored Rust wrapper has no lossless target mapping",
            )),
            _ => Err(unsupported(
                source,
                "the authored Rust type has no lossless target mapping",
            )),
        }
    }

    fn resolve_named(
        &self,
        spelling: &str,
        source: Option<SourceCoordinate>,
    ) -> Result<RustModuleType, ModuleDiagnostic> {
        let spelling = spelling.replace(' ', "");
        let name = final_segment(&spelling);
        let exact = RustSymbol::new(spelling.clone())
            .ok()
            .and_then(|symbol| self.discovery.declarations.get(&symbol));
        let suffix = format!("::{spelling}");
        let local = exact.into_iter().collect::<Vec<_>>();
        let local = if local.is_empty() {
            let qualified = self
                .discovery
                .declarations
                .values()
                .filter(|declaration| declaration.rust_symbol.as_str().ends_with(&suffix))
                .collect::<Vec<_>>();
            if qualified.is_empty() {
                self.discovery
                    .declarations
                    .values()
                    .filter(|declaration| final_segment(declaration.rust_symbol.as_str()) == name)
                    .collect::<Vec<_>>()
            } else {
                qualified
            }
        } else {
            local
        };
        if let [declaration] = local.as_slice() {
            return Ok(match declaration.kind {
                AuthoringDeclarationKind::Object => {
                    RustModuleType::LocalObject(declaration.rust_symbol.clone())
                }
                AuthoringDeclarationKind::Interface => {
                    RustModuleType::LocalInterface(declaration.rust_symbol.clone())
                }
                AuthoringDeclarationKind::Enum => {
                    RustModuleType::LocalEnum(declaration.rust_symbol.clone())
                }
                AuthoringDeclarationKind::Scalar => {
                    RustModuleType::CustomScalar(declaration.rust_symbol.clone())
                }
            });
        }
        let generated = self
            .discovery
            .generated_types
            .values()
            .filter(|binding| {
                let path = binding.rust_path.replace(' ', "");
                path == spelling || path.ends_with(&suffix) || final_segment(&path) == name
            })
            .collect::<Vec<_>>();
        match generated.as_slice() {
            [binding] => Ok(match binding.kind {
                GeneratedTypeKind::Object => {
                    RustModuleType::GeneratedObject(binding.target_name.clone())
                }
                GeneratedTypeKind::Interface => {
                    RustModuleType::GeneratedInterface(binding.target_name.clone())
                }
            }),
            _ => Err(unsupported(
                source,
                "the authored Rust type is unresolved or ambiguous",
            )),
        }
    }

    fn expand_alias(
        &self,
        path: &syn::TypePath,
        source: Option<SourceCoordinate>,
    ) -> Result<Option<Type>, ModuleDiagnostic> {
        let spelling = path
            .path
            .segments
            .iter()
            .map(|segment| segment.ident.to_string())
            .collect::<Vec<_>>()
            .join("::");
        let name = final_segment(&spelling);
        let suffix = format!("::{spelling}");
        let aliases = self
            .discovery
            .type_aliases
            .values()
            .filter(|alias| {
                alias.rust_path == spelling
                    || alias.rust_path.ends_with(&suffix)
                    || final_segment(&alias.rust_path) == name
            })
            .collect::<Vec<_>>();
        if aliases.is_empty() {
            return Ok(None);
        }
        if aliases.len() != 1 {
            return Err(unsupported(source, "the authored type alias is ambiguous"));
        }
        let alias = aliases[0];
        let segment = path
            .path
            .segments
            .last()
            .expect("a parsed type path has one segment");
        let arguments = match &segment.arguments {
            PathArguments::None => Vec::new(),
            PathArguments::AngleBracketed(arguments) => arguments
                .args
                .iter()
                .filter_map(|argument| match argument {
                    GenericArgument::Type(ty) => Some(ty.clone()),
                    _ => None,
                })
                .collect(),
            PathArguments::Parenthesized(_) => {
                return Err(unsupported(
                    source,
                    "parenthesized type aliases are unsupported",
                ));
            }
        };
        if arguments.len() != alias.parameters.len() {
            return Err(unsupported(
                source,
                "a type alias is not fully applied with concrete type arguments",
            ));
        }
        let mut target = syn::parse_str::<Type>(&alias.target)
            .map_err(|_| unsupported(source.clone(), "a type alias target could not be parsed"))?;
        let substitutions = alias
            .parameters
            .iter()
            .cloned()
            .zip(arguments)
            .collect::<BTreeMap<_, _>>();
        substitute_type(&mut target, &substitutions);
        Ok(Some(target))
    }
}

fn substitute_type(ty: &mut Type, substitutions: &BTreeMap<String, Type>) {
    match ty {
        Type::Path(path) if path.qself.is_none() => {
            if path.path.segments.len() == 1 {
                let segment = &path.path.segments[0];
                if matches!(segment.arguments, PathArguments::None)
                    && let Some(replacement) = substitutions.get(&segment.ident.to_string())
                {
                    *ty = replacement.clone();
                    return;
                }
            }
            for segment in &mut path.path.segments {
                if let PathArguments::AngleBracketed(arguments) = &mut segment.arguments {
                    for argument in &mut arguments.args {
                        if let GenericArgument::Type(argument) = argument {
                            substitute_type(argument, substitutions);
                        }
                    }
                }
            }
        }
        Type::Reference(reference) => substitute_type(&mut reference.elem, substitutions),
        Type::Tuple(tuple) => {
            for element in &mut tuple.elems {
                substitute_type(element, substitutions);
            }
        }
        Type::Array(array) => substitute_type(&mut array.elem, substitutions),
        Type::Slice(slice) => substitute_type(&mut slice.elem, substitutions),
        Type::Paren(paren) => substitute_type(&mut paren.elem, substitutions),
        Type::Group(group) => substitute_type(&mut group.elem, substitutions),
        Type::Ptr(pointer) => substitute_type(&mut pointer.elem, substitutions),
        _ => {}
    }
}

fn no_arguments(arguments: &PathArguments) -> bool {
    matches!(arguments, PathArguments::None)
}

fn one_type_argument(arguments: &PathArguments) -> Option<&Type> {
    let PathArguments::AngleBracketed(arguments) = arguments else {
        return None;
    };
    let mut types = arguments.args.iter().filter_map(|argument| match argument {
        GenericArgument::Type(ty) => Some(ty),
        _ => None,
    });
    let first = types.next()?;
    types.next().is_none().then_some(first)
}

/// Closed engine-independent value corresponding to [`RustModuleType`].
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ModuleValue {
    /// Owned string.
    String(String),
    /// Target integer.
    Integer(i64),
    /// Boolean.
    Boolean(bool),
    /// Finite JSON number used for a target float.
    Float(Number),
    /// Target Void.
    Void,
    /// Ordered recursive list.
    List(Vec<ModuleValue>),
    /// Explicit optional presence/null.
    Optional(Option<Box<ModuleValue>>),
    /// Local object persistent state keyed by Rust field name.
    LocalObject {
        /// Exact local object type.
        ty: RustSymbol,
        /// Typed persistent fields.
        state: BTreeMap<String, ModuleValue>,
    },
    /// Local interface handle with preserved concrete identity.
    LocalInterface {
        /// Exact interface type.
        interface: RustSymbol,
        /// Exact accepted concrete object.
        concrete: RustSymbol,
        /// Target-compatible identifier.
        id: String,
    },
    /// Exact local enum member.
    LocalEnum {
        /// Exact enum type.
        ty: RustSymbol,
        /// Canonical wire member.
        member: WireName,
    },
    /// Typed transparent scalar value.
    CustomScalar {
        /// Exact scalar type.
        ty: RustSymbol,
        /// Typed underlying representation.
        value: Box<ModuleValue>,
    },
    /// Checked generated object handle.
    GeneratedObject { target_name: String, id: String },
    /// Checked generated interface handle.
    GeneratedInterface { target_name: String, id: String },
}

/// Exposure and persistence policy for one object field.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ObjectFieldMode {
    /// Public TypeDef field and persistent state.
    Exposed,
    /// Private persistent state.
    Persistent,
    /// Omitted implementation detail reconstructed with `Default`.
    Transient,
}

/// One descriptor-owned local object field policy.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ObjectFieldContract {
    /// Rust field identifier used by generated access bridges.
    pub rust_name: String,
    /// Shared TypeDef/state wire name.
    pub wire_name: WireName,
    /// Closed recursive field type.
    pub ty: RustModuleType,
    /// Exposure/persistence decision.
    pub mode: ObjectFieldMode,
}

/// The only safe root construction choices.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ConstructionPolicy {
    /// One explicit constructor; application failure remains observable.
    Explicit { fallible: bool },
    /// Explicitly declared normal Rust `Default` construction.
    Default,
}

/// Complete local object state contract.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ObjectContract {
    /// Canonical local object symbol.
    pub symbol: RustSymbol,
    /// Field policies keyed by unique Rust name.
    pub fields: Vec<ObjectFieldContract>,
    /// Root construction policy when this object is the module root.
    pub construction: Option<ConstructionPolicy>,
}

impl ObjectContract {
    /// Validates unique Rust/wire fields and a present root construction policy.
    pub fn validate(&self, is_root: bool) -> Result<(), ModuleDiagnostic> {
        let rust_names = self
            .fields
            .iter()
            .map(|field| field.rust_name.as_str())
            .collect::<BTreeSet<_>>();
        let wire_names = self
            .fields
            .iter()
            .filter(|field| field.mode != ObjectFieldMode::Transient)
            .map(|field| field.wire_name.as_str())
            .collect::<BTreeSet<_>>();
        if rust_names.len() != self.fields.len()
            || wire_names.len()
                != self
                    .fields
                    .iter()
                    .filter(|field| field.mode != ObjectFieldMode::Transient)
                    .count()
        {
            return Err(diagnostic(
                ModuleDiagnosticCode::StateShapeInvalid,
                None,
                "object state fields are not uniquely addressable",
                "use unique Rust and persistent wire field names",
            ));
        }
        if is_root != self.construction.is_some() {
            return Err(diagnostic(
                ModuleDiagnosticCode::ConstructorInvalid,
                None,
                "root construction policy is missing or attached to a non-root object",
                "declare exactly one explicit constructor or root default policy",
            ));
        }
        Ok(())
    }

    /// Returns only fields visible in the public object TypeDef.
    pub fn exposed_fields(&self) -> impl Iterator<Item = &ObjectFieldContract> {
        self.fields
            .iter()
            .filter(|field| field.mode == ObjectFieldMode::Exposed)
    }
}

/// One interface method shape used for implementation compatibility checks.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct InterfaceMethod {
    /// Canonical wire name.
    pub wire_name: WireName,
    /// Canonical argument and result signature.
    pub signature: String,
}

/// Closed local interface and its accepted concrete implementations.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct InterfaceContract {
    /// Canonical interface symbol.
    pub symbol: RustSymbol,
    /// Exact exported method set.
    pub methods: BTreeSet<InterfaceMethod>,
    /// Exact local implementation set.
    pub implementations: BTreeSet<RustSymbol>,
}

impl InterfaceContract {
    /// Requires every implementation to be a discovered object and every method name unique.
    pub fn validate(
        &self,
        declarations: &BTreeMap<RustSymbol, AuthoringDeclaration>,
    ) -> Result<(), ModuleDiagnostic> {
        if self.implementations.iter().any(|symbol| {
            declarations
                .get(symbol)
                .is_none_or(|declaration| declaration.kind != AuthoringDeclarationKind::Object)
        }) {
            return Err(diagnostic(
                ModuleDiagnosticCode::InterfaceInvalid,
                None,
                "an interface implementation is not a discovered local object",
                "retain only explicit implementations by discovered objects",
            ));
        }
        let names = self
            .methods
            .iter()
            .map(|method| method.wire_name.as_str())
            .collect::<BTreeSet<_>>();
        if names.len() != self.methods.len() {
            return Err(diagnostic(
                ModuleDiagnosticCode::InterfaceInvalid,
                None,
                "interface methods collide after wire-name normalization",
                "give every interface method a unique wire name",
            ));
        }
        Ok(())
    }
}

/// One unit enum variant and its optional explicit wire name.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EnumVariantContract {
    /// Authored Rust variant name.
    pub rust_name: String,
    /// Explicit wire spelling, otherwise target prefix normalization applies.
    pub explicit_wire_name: Option<WireName>,
}

/// Exact unit enum contract.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EnumContract {
    /// Canonical enum symbol.
    pub symbol: RustSymbol,
    /// Variants in descriptor order.
    pub variants: Vec<EnumVariantContract>,
}

impl EnumContract {
    /// Derives every target wire member and rejects empty/colliding contracts.
    pub fn wire_members(&self) -> Result<BTreeMap<String, WireName>, ModuleDiagnostic> {
        if self.variants.is_empty() {
            return Err(diagnostic(
                ModuleDiagnosticCode::EnumInvalid,
                None,
                "an exported enum has no unit variants",
                "declare at least one unit variant",
            ));
        }
        let enum_name = final_segment(self.symbol.as_str());
        let trim_prefix = self
            .variants
            .iter()
            .all(|variant| variant.rust_name.starts_with(enum_name));
        let mut members = BTreeMap::new();
        let mut seen = BTreeSet::new();
        for variant in &self.variants {
            let wire = if let Some(explicit) = &variant.explicit_wire_name {
                explicit.clone()
            } else {
                let spelling = if trim_prefix {
                    variant
                        .rust_name
                        .strip_prefix(enum_name)
                        .unwrap_or(&variant.rust_name)
                } else {
                    &variant.rust_name
                };
                WireName::new(spelling).map_err(|_| {
                    diagnostic(
                        ModuleDiagnosticCode::EnumInvalid,
                        None,
                        "enum prefix normalization produced an invalid member name",
                        "use an explicit valid wire name",
                    )
                })?
            };
            if !seen.insert(wire.clone()) {
                return Err(diagnostic(
                    ModuleDiagnosticCode::EnumInvalid,
                    None,
                    "enum variants collide on one wire member",
                    "give every enum variant a unique wire name",
                ));
            }
            members.insert(variant.rust_name.clone(), wire);
        }
        Ok(members)
    }
}

/// Transparent local scalar over one primitive representation.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ScalarContract {
    /// Canonical scalar symbol.
    pub symbol: RustSymbol,
    /// Exact primitive representation.
    pub representation: RustModuleType,
}

impl ScalarContract {
    /// Rejects transforming or structured scalar representations.
    pub fn validate(&self) -> Result<(), ModuleDiagnostic> {
        if matches!(
            self.representation,
            RustModuleType::String
                | RustModuleType::Integer
                | RustModuleType::Boolean
                | RustModuleType::Float
        ) {
            Ok(())
        } else {
            Err(diagnostic(
                ModuleDiagnosticCode::ScalarInvalid,
                None,
                "a custom scalar representation is not one transparent primitive",
                "use a one-field newtype over String, i64, bool, or f64",
            ))
        }
    }
}

/// Closed contract catalog required for local structured value codecs.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct TypeCatalog {
    /// Local object contracts.
    pub objects: BTreeMap<RustSymbol, ObjectContract>,
    /// Local interface contracts.
    pub interfaces: BTreeMap<RustSymbol, InterfaceContract>,
    /// Local enum contracts.
    pub enums: BTreeMap<RustSymbol, EnumContract>,
    /// Local scalar contracts.
    pub scalars: BTreeMap<RustSymbol, ScalarContract>,
}

impl TypeCatalog {
    /// Compiles every discovered local declaration into one closed type catalog.
    pub fn compile(discovery: &ModuleDiscovery) -> Result<Self, ModuleDiagnosticSet> {
        let resolver = TypeResolver::new(discovery);
        let mut catalog = Self::default();
        let mut diagnostics = Vec::new();

        for declaration in discovery.declarations.values() {
            let result = match declaration.kind {
                AuthoringDeclarationKind::Object => {
                    compile_object(declaration, discovery, &resolver).map(|contract| {
                        catalog
                            .objects
                            .insert(declaration.rust_symbol.clone(), contract);
                    })
                }
                AuthoringDeclarationKind::Interface => {
                    compile_interface(declaration, discovery, &resolver).map(|contract| {
                        catalog
                            .interfaces
                            .insert(declaration.rust_symbol.clone(), contract);
                    })
                }
                AuthoringDeclarationKind::Enum => compile_enum(declaration).map(|contract| {
                    catalog
                        .enums
                        .insert(declaration.rust_symbol.clone(), contract);
                }),
                AuthoringDeclarationKind::Scalar => {
                    compile_scalar(declaration, &resolver).map(|contract| {
                        catalog
                            .scalars
                            .insert(declaration.rust_symbol.clone(), contract);
                    })
                }
            };
            if let Err(error) = result {
                diagnostics.push(error);
            }
        }

        if diagnostics.is_empty() {
            Ok(catalog)
        } else {
            Err(ModuleDiagnosticSet::new(diagnostics)
                .expect("type catalog compilation collected at least one diagnostic"))
        }
    }
}

fn compile_object(
    declaration: &AuthoringDeclaration,
    discovery: &ModuleDiscovery,
    resolver: &TypeResolver<'_>,
) -> Result<ObjectContract, ModuleDiagnostic> {
    let mut fields = Vec::new();
    for field in &declaration.fields {
        if field.policy == super::authoring::AuthoringFieldPolicy::Transient {
            continue;
        }
        let ty = resolver.resolve(
            &field.rust_type,
            TypePosition::State,
            Some(field.source.clone()),
        )?;
        let wire_name = metadata_wire_name(
            field.metadata.get("rename"),
            &field.name,
            &field.source,
            ModuleDiagnosticCode::StateShapeInvalid,
        )?;
        fields.push(ObjectFieldContract {
            rust_name: field.name.clone(),
            wire_name,
            ty,
            mode: match field.policy {
                super::authoring::AuthoringFieldPolicy::Field => ObjectFieldMode::Exposed,
                super::authoring::AuthoringFieldPolicy::State => ObjectFieldMode::Persistent,
                super::authoring::AuthoringFieldPolicy::Transient => unreachable!(),
            },
        });
    }

    let constructors = declaration
        .functions
        .iter()
        .filter(|function| function.metadata.contains_key("constructor"))
        .collect::<Vec<_>>();
    let is_root = declaration.rust_symbol == discovery.root;
    let construction = match (is_root, constructors.as_slice()) {
        (true, [constructor]) => Some(ConstructionPolicy::Explicit {
            fallible: constructor
                .output
                .trim_start_matches("->")
                .trim()
                .split("::")
                .last()
                .is_some_and(|output| output.starts_with("Result")),
        }),
        (true, []) if metadata_bool(declaration.metadata.get("default")) == Some(true) => {
            Some(ConstructionPolicy::Default)
        }
        (true, _) => {
            return Err(diagnostic(
                ModuleDiagnosticCode::ConstructorInvalid,
                Some(declaration.source.clone()),
                "the module root does not have exactly one safe construction policy",
                "declare one constructor or explicit default root construction",
            ));
        }
        (false, []) => None,
        (false, _) => {
            return Err(diagnostic(
                ModuleDiagnosticCode::ConstructorInvalid,
                Some(constructors[0].source.clone()),
                "a non-root object declares a module constructor",
                "move the constructor to the explicit module root",
            ));
        }
    };
    let contract = ObjectContract {
        symbol: declaration.rust_symbol.clone(),
        fields,
        construction,
    };
    contract.validate(is_root)?;
    Ok(contract)
}

fn compile_interface(
    declaration: &AuthoringDeclaration,
    discovery: &ModuleDiscovery,
    resolver: &TypeResolver<'_>,
) -> Result<InterfaceContract, ModuleDiagnostic> {
    let mut methods = BTreeSet::new();
    for method in &declaration.interface_methods {
        let mut argument_types = Vec::new();
        for parameter in &method.parameters {
            argument_types.push(
                resolver
                    .resolve(
                        &parameter.rust_type,
                        TypePosition::Input,
                        Some(parameter.source.clone()),
                    )?
                    .projected(),
            );
        }
        let output = method.output.trim().trim_start_matches("->").trim();
        let output = if output.is_empty() {
            RustModuleType::Void
        } else {
            let output_type = syn::parse_str::<Type>(output).map_err(|_| {
                diagnostic(
                    ModuleDiagnosticCode::InterfaceInvalid,
                    Some(method.source.clone()),
                    "an interface return type could not be parsed",
                    "use one concrete supported return type",
                )
            })?;
            let success = result_success_type(&output_type).unwrap_or(&output_type);
            resolver.resolve(
                &success.to_token_stream().to_string(),
                TypePosition::Output,
                Some(method.source.clone()),
            )?
        };
        let wire_name = metadata_wire_name(
            method.metadata.get("rename"),
            &method.name,
            &method.source,
            ModuleDiagnosticCode::InterfaceInvalid,
        )?;
        let signature = format!("{argument_types:?}->{:?}", output.projected());
        if !methods.insert(InterfaceMethod {
            wire_name,
            signature,
        }) {
            return Err(diagnostic(
                ModuleDiagnosticCode::InterfaceInvalid,
                Some(method.source.clone()),
                "an interface method shape occurs more than once",
                "retain one uniquely named interface method",
            ));
        }
    }
    let contract = InterfaceContract {
        symbol: declaration.rust_symbol.clone(),
        methods,
        implementations: discovery
            .interface_implementations
            .get(&declaration.rust_symbol)
            .cloned()
            .unwrap_or_default(),
    };
    contract.validate(&discovery.declarations)?;
    Ok(contract)
}

fn result_success_type(ty: &Type) -> Option<&Type> {
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
    let types = arguments
        .args
        .iter()
        .filter_map(|argument| match argument {
            GenericArgument::Type(ty) => Some(ty),
            _ => None,
        })
        .collect::<Vec<_>>();
    (types.len() == 2).then_some(types[0])
}

fn compile_enum(declaration: &AuthoringDeclaration) -> Result<EnumContract, ModuleDiagnostic> {
    let variants = declaration
        .variants
        .iter()
        .map(|variant| {
            let explicit_wire_name = variant
                .metadata
                .get("rename")
                .map(|value| {
                    metadata_string(value).and_then(|value| {
                        WireName::new(value).map_err(|_| {
                            diagnostic(
                                ModuleDiagnosticCode::EnumInvalid,
                                Some(variant.source.clone()),
                                "an explicit enum wire member is invalid",
                                "use one valid target enum member name",
                            )
                        })
                    })
                })
                .transpose()?;
            Ok(EnumVariantContract {
                rust_name: variant.name.clone(),
                explicit_wire_name,
            })
        })
        .collect::<Result<Vec<_>, ModuleDiagnostic>>()?;
    let contract = EnumContract {
        symbol: declaration.rust_symbol.clone(),
        variants,
    };
    contract.wire_members()?;
    Ok(contract)
}

fn compile_scalar(
    declaration: &AuthoringDeclaration,
    resolver: &TypeResolver<'_>,
) -> Result<ScalarContract, ModuleDiagnostic> {
    let representation = declaration.scalar_representation.as_ref().ok_or_else(|| {
        diagnostic(
            ModuleDiagnosticCode::ScalarInvalid,
            Some(declaration.source.clone()),
            "a scalar representation is absent from the normalized source model",
            "use one transparent supported tuple newtype",
        )
    })?;
    let contract = ScalarContract {
        symbol: declaration.rust_symbol.clone(),
        representation: resolver.resolve(
            representation,
            TypePosition::State,
            Some(declaration.source.clone()),
        )?,
    };
    contract.validate()?;
    Ok(contract)
}

fn metadata_wire_name(
    explicit: Option<&String>,
    rust_name: &str,
    source: &SourceCoordinate,
    code: ModuleDiagnosticCode,
) -> Result<WireName, ModuleDiagnostic> {
    let value = explicit
        .map(|value| {
            metadata_string(value).map_err(|_| {
                diagnostic(
                    code,
                    Some(source.clone()),
                    "an explicit wire name is not a Rust string literal",
                    "use one valid quoted target identifier",
                )
            })
        })
        .transpose()?
        .unwrap_or_else(|| rust_name.to_case(Case::Camel));
    WireName::new(value).map_err(|_| {
        diagnostic(
            code,
            Some(source.clone()),
            "a normalized wire name is invalid",
            "use one valid target identifier",
        )
    })
}

fn metadata_string(value: &str) -> Result<String, ModuleDiagnostic> {
    syn::parse_str::<syn::LitStr>(value)
        .map(|value| value.value())
        .map_err(|_| {
            diagnostic(
                ModuleDiagnosticCode::MetadataMalformed,
                None,
                "string metadata is not a Rust string literal",
                "use one quoted string literal",
            )
        })
}

fn metadata_bool(value: Option<&String>) -> Option<bool> {
    value
        .and_then(|value| syn::parse_str::<syn::LitBool>(value).ok())
        .map(|value| value.value)
}

/// Stateless codec over one immutable closed type catalog.
pub struct ModuleValueCodec<'a> {
    catalog: &'a TypeCatalog,
}

impl<'a> ModuleValueCodec<'a> {
    /// Creates one codec whose local shapes cannot change during a conversion.
    #[must_use]
    pub const fn new(catalog: &'a TypeCatalog) -> Self {
        Self { catalog }
    }

    /// Encodes one typed value to its exact target JSON representation.
    pub fn encode(
        &self,
        ty: &RustModuleType,
        value: &ModuleValue,
    ) -> Result<Value, ModuleDiagnostic> {
        match (ty, value) {
            (RustModuleType::String, ModuleValue::String(value)) => {
                Ok(Value::String(value.clone()))
            }
            (RustModuleType::Integer, ModuleValue::Integer(value)) => {
                Ok(Value::Number((*value).into()))
            }
            (RustModuleType::Boolean, ModuleValue::Boolean(value)) => Ok(Value::Bool(*value)),
            (RustModuleType::Float, ModuleValue::Float(value))
                if value.as_f64().is_some_and(f64::is_finite) =>
            {
                Ok(Value::Number(value.clone()))
            }
            (RustModuleType::Void, ModuleValue::Void) => Ok(Value::Null),
            (RustModuleType::List(element), ModuleValue::List(values)) => values
                .iter()
                .map(|value| self.encode(element, value))
                .collect::<Result<Vec<_>, _>>()
                .map(Value::Array),
            (RustModuleType::Optional(_), ModuleValue::Optional(None)) => Ok(Value::Null),
            (RustModuleType::Optional(inner), ModuleValue::Optional(Some(value))) => {
                self.encode(inner, value)
            }
            (RustModuleType::LocalObject(symbol), ModuleValue::LocalObject { ty, state })
                if symbol == ty =>
            {
                self.encode_object(symbol, state)
            }
            (
                RustModuleType::LocalInterface(symbol),
                ModuleValue::LocalInterface {
                    interface,
                    concrete,
                    id,
                },
            ) if symbol == interface => self.encode_interface(symbol, concrete, id),
            (RustModuleType::LocalEnum(symbol), ModuleValue::LocalEnum { ty, member })
                if symbol == ty =>
            {
                self.encode_enum(symbol, member)
            }
            (RustModuleType::CustomScalar(symbol), ModuleValue::CustomScalar { ty, value })
                if symbol == ty =>
            {
                let scalar = self
                    .catalog
                    .scalars
                    .get(symbol)
                    .ok_or_else(|| value_error("custom scalar contract is absent"))?;
                scalar.validate()?;
                self.encode(&scalar.representation, value)
            }
            (
                RustModuleType::GeneratedObject(name),
                ModuleValue::GeneratedObject { target_name, id },
            ) if name == target_name => encode_id(id),
            (
                RustModuleType::GeneratedInterface(name),
                ModuleValue::GeneratedInterface { target_name, id },
            ) if name == target_name => encode_id(id),
            _ => Err(value_error(
                "typed value does not match its declared module type",
            )),
        }
    }

    /// Decodes target JSON without coercing wrong kinds or numeric ranges.
    pub fn decode(
        &self,
        ty: &RustModuleType,
        value: &Value,
    ) -> Result<ModuleValue, ModuleDiagnostic> {
        match ty {
            RustModuleType::String => value
                .as_str()
                .map(|value| ModuleValue::String(value.to_owned()))
                .ok_or_else(|| value_error("expected a JSON string")),
            RustModuleType::Integer => value.as_i64().map(ModuleValue::Integer).ok_or_else(|| {
                diagnostic(
                    ModuleDiagnosticCode::NumericOutOfRange,
                    None,
                    "expected a target-range JSON integer",
                    "supply a signed 64-bit integer",
                )
            }),
            RustModuleType::Boolean => value
                .as_bool()
                .map(ModuleValue::Boolean)
                .ok_or_else(|| value_error("expected a JSON boolean")),
            RustModuleType::Float => value
                .as_f64()
                .filter(|value| value.is_finite())
                .map(|_| {
                    ModuleValue::Float(
                        value
                            .as_number()
                            .expect("a successful numeric decode retains its number")
                            .clone(),
                    )
                })
                .ok_or_else(|| {
                    diagnostic(
                        ModuleDiagnosticCode::NumericOutOfRange,
                        None,
                        "expected a finite target JSON number",
                        "supply a finite JSON number",
                    )
                }),
            RustModuleType::Void if value.is_null() => Ok(ModuleValue::Void),
            RustModuleType::Void => Err(value_error("target Void must be JSON null")),
            RustModuleType::List(element) => value
                .as_array()
                .ok_or_else(|| value_error("expected a JSON list"))?
                .iter()
                .map(|value| self.decode(element, value))
                .collect::<Result<Vec<_>, _>>()
                .map(ModuleValue::List),
            RustModuleType::Optional(_) if value.is_null() => Ok(ModuleValue::Optional(None)),
            RustModuleType::Optional(inner) => self
                .decode(inner, value)
                .map(|value| ModuleValue::Optional(Some(Box::new(value)))),
            RustModuleType::LocalObject(symbol) => self.decode_object(symbol, value),
            RustModuleType::LocalInterface(symbol) => self.decode_interface(symbol, value),
            RustModuleType::LocalEnum(symbol) => self.decode_enum(symbol, value),
            RustModuleType::CustomScalar(symbol) => {
                let scalar = self
                    .catalog
                    .scalars
                    .get(symbol)
                    .ok_or_else(|| value_error("custom scalar contract is absent"))?;
                scalar.validate()?;
                self.decode(&scalar.representation, value)
                    .map(|value| ModuleValue::CustomScalar {
                        ty: symbol.clone(),
                        value: Box::new(value),
                    })
            }
            RustModuleType::GeneratedObject(name) => {
                decode_id(value).map(|id| ModuleValue::GeneratedObject {
                    target_name: name.clone(),
                    id,
                })
            }
            RustModuleType::GeneratedInterface(name) => {
                decode_id(value).map(|id| ModuleValue::GeneratedInterface {
                    target_name: name.clone(),
                    id,
                })
            }
        }
    }

    fn encode_object(
        &self,
        symbol: &RustSymbol,
        state: &BTreeMap<String, ModuleValue>,
    ) -> Result<Value, ModuleDiagnostic> {
        let contract = self
            .catalog
            .objects
            .get(symbol)
            .ok_or_else(|| value_error("local object contract is absent"))?;
        let mut encoded = Map::new();
        for field in &contract.fields {
            if field.mode == ObjectFieldMode::Transient {
                continue;
            }
            let value = state
                .get(&field.rust_name)
                .ok_or_else(|| value_error("persistent object state field is absent"))?;
            encoded.insert(field.wire_name.to_string(), self.encode(&field.ty, value)?);
        }
        if state.keys().any(|name| {
            !contract
                .fields
                .iter()
                .any(|field| field.mode != ObjectFieldMode::Transient && &field.rust_name == name)
        }) {
            return Err(value_error(
                "object state contains an unknown or transient field",
            ));
        }
        Ok(Value::Object(encoded))
    }

    fn decode_object(
        &self,
        symbol: &RustSymbol,
        value: &Value,
    ) -> Result<ModuleValue, ModuleDiagnostic> {
        let contract = self
            .catalog
            .objects
            .get(symbol)
            .ok_or_else(|| value_error("local object contract is absent"))?;
        let object = value
            .as_object()
            .ok_or_else(|| value_error("local object state must be a JSON object"))?;
        let admitted = contract
            .fields
            .iter()
            .filter(|field| field.mode != ObjectFieldMode::Transient)
            .map(|field| field.wire_name.as_str())
            .collect::<BTreeSet<_>>();
        if object.keys().any(|name| !admitted.contains(name.as_str())) {
            return Err(value_error(
                "local object state contains an unknown wire field",
            ));
        }
        let mut state = BTreeMap::new();
        for field in &contract.fields {
            if field.mode == ObjectFieldMode::Transient {
                continue;
            }
            let value = object
                .get(field.wire_name.as_str())
                .ok_or_else(|| value_error("persistent object state field is absent"))?;
            state.insert(field.rust_name.clone(), self.decode(&field.ty, value)?);
        }
        Ok(ModuleValue::LocalObject {
            ty: symbol.clone(),
            state,
        })
    }

    fn encode_interface(
        &self,
        interface: &RustSymbol,
        concrete: &RustSymbol,
        id: &str,
    ) -> Result<Value, ModuleDiagnostic> {
        let contract = self
            .catalog
            .interfaces
            .get(interface)
            .ok_or_else(|| value_error("local interface contract is absent"))?;
        if !contract.implementations.contains(concrete) || id.is_empty() {
            return Err(diagnostic(
                ModuleDiagnosticCode::InterfaceInvalid,
                None,
                "interface value has an unaccepted concrete identity or empty ID",
                "use one declared implementation and target ID",
            ));
        }
        Ok(Value::Object(Map::from_iter([
            ("id".to_owned(), Value::String(id.to_owned())),
            ("concrete".to_owned(), Value::String(concrete.to_string())),
        ])))
    }

    fn decode_interface(
        &self,
        interface: &RustSymbol,
        value: &Value,
    ) -> Result<ModuleValue, ModuleDiagnostic> {
        let contract = self
            .catalog
            .interfaces
            .get(interface)
            .ok_or_else(|| value_error("local interface contract is absent"))?;
        let object = value
            .as_object()
            .ok_or_else(|| value_error("interface value must be a JSON object"))?;
        if object.len() != 2 {
            return Err(value_error(
                "interface value must contain only id and concrete identity",
            ));
        }
        let id = object
            .get("id")
            .and_then(Value::as_str)
            .filter(|id| !id.is_empty())
            .ok_or_else(|| value_error("interface ID is absent or invalid"))?;
        let concrete = object
            .get("concrete")
            .and_then(Value::as_str)
            .ok_or_else(|| value_error("interface concrete identity is absent"))?;
        let concrete = RustSymbol::new(concrete)
            .map_err(|_| value_error("interface concrete identity is invalid"))?;
        if !contract.implementations.contains(&concrete) {
            return Err(diagnostic(
                ModuleDiagnosticCode::InterfaceInvalid,
                None,
                "interface concrete identity is not an accepted implementation",
                "use one declared local implementation",
            ));
        }
        Ok(ModuleValue::LocalInterface {
            interface: interface.clone(),
            concrete,
            id: id.to_owned(),
        })
    }

    fn encode_enum(
        &self,
        symbol: &RustSymbol,
        member: &WireName,
    ) -> Result<Value, ModuleDiagnostic> {
        let contract = self
            .catalog
            .enums
            .get(symbol)
            .ok_or_else(|| value_error("local enum contract is absent"))?;
        if contract
            .wire_members()?
            .values()
            .any(|known| known == member)
        {
            Ok(Value::String(member.to_string()))
        } else {
            Err(diagnostic(
                ModuleDiagnosticCode::EnumInvalid,
                None,
                "enum value is not one declared wire member",
                "use one exact declared enum member",
            ))
        }
    }

    fn decode_enum(
        &self,
        symbol: &RustSymbol,
        value: &Value,
    ) -> Result<ModuleValue, ModuleDiagnostic> {
        let member = value
            .as_str()
            .ok_or_else(|| value_error("enum value must be a JSON string"))?;
        let member =
            WireName::new(member).map_err(|_| value_error("enum wire member is invalid"))?;
        self.encode_enum(symbol, &member)?;
        Ok(ModuleValue::LocalEnum {
            ty: symbol.clone(),
            member,
        })
    }
}

fn encode_id(id: &str) -> Result<Value, ModuleDiagnostic> {
    if id.is_empty() {
        Err(value_error("generated handle ID must be non-empty"))
    } else {
        Ok(Value::String(id.to_owned()))
    }
}

fn decode_id(value: &Value) -> Result<String, ModuleDiagnostic> {
    value
        .as_str()
        .filter(|id| !id.is_empty())
        .map(str::to_owned)
        .ok_or_else(|| value_error("generated handle ID must be a non-empty JSON string"))
}

/// Classification used by the exhaustive public type-policy manifest.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TypePolicyDisposition {
    /// Same target semantics and an equivalent Rust form.
    Equivalent,
    /// Same target semantics through a deliberately idiomatic Rust form.
    IdiomaticEquivalent,
    /// The target module contract cannot represent the source distinction.
    UnsupportedByTarget,
    /// Owned by a later reviewed feature rather than silently accepted.
    DeferredWithOwner,
}

/// One reviewed source/target type-policy row.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct TypePolicyRow {
    /// Stable source category.
    pub category: &'static str,
    /// Rust authoring form.
    pub rust_form: &'static str,
    /// Reviewed disposition.
    pub disposition: TypePolicyDisposition,
    /// Owning boundary or rationale.
    pub rationale: &'static str,
}

/// Returns the complete closed type-policy inventory used by the compiler.
#[must_use]
pub fn rust_type_policy_manifest() -> &'static [TypePolicyRow] {
    TYPE_POLICY_MANIFEST
}

use TypePolicyDisposition::{Equivalent, IdiomaticEquivalent, UnsupportedByTarget};

const TYPE_POLICY_MANIFEST: &[TypePolicyRow] = &[
    row("string", "String", Equivalent, "owned UTF-8 scalar"),
    row("integer", "i64", Equivalent, "target signed integer"),
    row("boolean", "bool", Equivalent, "exact JSON boolean"),
    row("float", "f64", Equivalent, "finite target number"),
    row(
        "void",
        "() return",
        IdiomaticEquivalent,
        "Rust unit maps to target Void",
    ),
    row(
        "list",
        "Vec<T>",
        IdiomaticEquivalent,
        "owned ordered recursive list",
    ),
    row(
        "optional",
        "Option<T>",
        IdiomaticEquivalent,
        "explicit omission/nullability",
    ),
    row(
        "result",
        "Result<T, E> return",
        IdiomaticEquivalent,
        "function outcome rather than TypeDef wrapper",
    ),
    row(
        "local-object",
        "exported struct",
        IdiomaticEquivalent,
        "descriptor-owned typed state",
    ),
    row(
        "local-interface",
        "exported trait",
        IdiomaticEquivalent,
        "closed concrete identity handle",
    ),
    row(
        "local-enum",
        "unit enum",
        IdiomaticEquivalent,
        "exact member codec",
    ),
    row(
        "local-scalar",
        "transparent newtype",
        IdiomaticEquivalent,
        "lossless primitive representation",
    ),
    row(
        "generated-object",
        "checked generated handle",
        Equivalent,
        "target expected-type ID",
    ),
    row(
        "generated-interface",
        "checked generated interface",
        Equivalent,
        "target ID and interface identity",
    ),
    row(
        "borrowed-value",
        "&T",
        UnsupportedByTarget,
        "call-external lifetime is not representable",
    ),
    row(
        "tuple-map-union",
        "tuple/map/union",
        UnsupportedByTarget,
        "no implicit structural or JSON fallback",
    ),
    row(
        "other-integers",
        "integer except i64",
        UnsupportedByTarget,
        "no silent range narrowing",
    ),
    row(
        "nested-option",
        "Option<Option<T>>",
        UnsupportedByTarget,
        "target nullability would collapse a distinction",
    ),
];

const fn row(
    category: &'static str,
    rust_form: &'static str,
    disposition: TypePolicyDisposition,
    rationale: &'static str,
) -> TypePolicyRow {
    TypePolicyRow {
        category,
        rust_form,
        disposition,
        rationale,
    }
}

fn final_segment(path: &str) -> &str {
    path.rsplit("::").next().unwrap_or(path)
}

fn unsupported(source: Option<SourceCoordinate>, message: &'static str) -> ModuleDiagnostic {
    diagnostic(
        ModuleDiagnosticCode::TypeUnsupported,
        source,
        message,
        "use one concrete type from the closed module type policy",
    )
}

fn value_error(message: &'static str) -> ModuleDiagnostic {
    diagnostic(
        ModuleDiagnosticCode::ValueDecodeFailed,
        None,
        message,
        "supply a value matching the exact declared module type",
    )
}

fn diagnostic(
    code: ModuleDiagnosticCode,
    source: Option<SourceCoordinate>,
    message: &'static str,
    remediation: &'static str,
) -> ModuleDiagnostic {
    ModuleDiagnostic::new(code, source, message, remediation)
        .expect("reviewed type diagnostics satisfy the safe renderer policy")
}

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::*;

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(256))]

        // Persistent fields round-trip under one shared wire policy while transient fields remain absent.
        #[test]
        fn property_05_object_state_construction_lossless_safe(
            value in ".{0,32}",
            renamed in any::<bool>(),
            private in any::<bool>(),
        ) {
            let symbol = symbol("crate::Root");
            let wire = WireName::new(if renamed { "renamed" } else { "value" }).unwrap();
            let contract = ObjectContract {
                symbol: symbol.clone(),
                fields: vec![
                    ObjectFieldContract {
                        rust_name: "value".to_owned(),
                        wire_name: wire.clone(),
                        ty: RustModuleType::String,
                        mode: if private { ObjectFieldMode::Persistent } else { ObjectFieldMode::Exposed },
                    },
                    ObjectFieldContract {
                        rust_name: "cache".to_owned(),
                        wire_name: WireName::new("cache").unwrap(),
                        ty: RustModuleType::Integer,
                        mode: ObjectFieldMode::Transient,
                    },
                ],
                construction: Some(ConstructionPolicy::Explicit { fallible: false }),
            };
            contract.validate(true).unwrap();
            prop_assert_eq!(contract.exposed_fields().count(), usize::from(!private));
            let catalog = TypeCatalog { objects: BTreeMap::from([(symbol.clone(), contract)]), ..TypeCatalog::default() };
            let codec = ModuleValueCodec::new(&catalog);
            let original = ModuleValue::LocalObject {
                ty: symbol.clone(),
                state: BTreeMap::from([("value".to_owned(), ModuleValue::String(value))]),
            };
            let encoded = codec.encode(&RustModuleType::LocalObject(symbol.clone()), &original).unwrap();
            prop_assert_eq!(encoded.as_object().unwrap().len(), 1);
            prop_assert!(encoded.get(wire.as_str()).is_some());
            prop_assert_eq!(codec.decode(&RustModuleType::LocalObject(symbol), &encoded).unwrap(), original);
        }

        // Interface handles retain both the target ID and one accepted concrete local identity.
        #[test]
        fn property_06_interface_projection_identity_closed(
            id in "[A-Za-z0-9_-]{1,32}",
            implementation_count in 1_usize..8,
            selected in any::<usize>(),
        ) {
            let interface = symbol("crate::Named");
            let implementations = (0..implementation_count)
                .map(|index| symbol(&format!("crate::Object{index}")))
                .collect::<BTreeSet<_>>();
            let concrete = implementations.iter().nth(selected % implementation_count).unwrap().clone();
            let contract = InterfaceContract {
                symbol: interface.clone(),
                methods: BTreeSet::from([InterfaceMethod { wire_name: WireName::new("name").unwrap(), signature: "() -> String".to_owned() }]),
                implementations,
            };
            let catalog = TypeCatalog { interfaces: BTreeMap::from([(interface.clone(), contract)]), ..TypeCatalog::default() };
            let codec = ModuleValueCodec::new(&catalog);
            let value = ModuleValue::LocalInterface { interface: interface.clone(), concrete, id };
            let encoded = codec.encode(&RustModuleType::LocalInterface(interface.clone()), &value).unwrap();
            prop_assert_eq!(codec.decode(&RustModuleType::LocalInterface(interface), &encoded).unwrap(), value);
        }

        // Enum normalization and transparent scalar codecs preserve one exact wire representation.
        #[test]
        fn property_07_enum_scalar_codecs_exact(
            selected in 0_usize..3,
            scalar in any::<i64>(),
        ) {
            let enum_symbol = symbol("crate::Mood");
            let enum_contract = EnumContract {
                symbol: enum_symbol.clone(),
                variants: ["MoodHappy", "MoodQuiet", "MoodBusy"].into_iter().map(|name| EnumVariantContract { rust_name: name.to_owned(), explicit_wire_name: None }).collect(),
            };
            let members = enum_contract.wire_members().unwrap();
            let member = members.values().nth(selected).unwrap().clone();
            let scalar_symbol = symbol("crate::Counter");
            let scalar_contract = ScalarContract { symbol: scalar_symbol.clone(), representation: RustModuleType::Integer };
            let catalog = TypeCatalog {
                enums: BTreeMap::from([(enum_symbol.clone(), enum_contract)]),
                scalars: BTreeMap::from([(scalar_symbol.clone(), scalar_contract)]),
                ..TypeCatalog::default()
            };
            let codec = ModuleValueCodec::new(&catalog);
            let enum_value = ModuleValue::LocalEnum { ty: enum_symbol.clone(), member };
            let encoded_enum = codec.encode(&RustModuleType::LocalEnum(enum_symbol.clone()), &enum_value).unwrap();
            prop_assert_eq!(codec.decode(&RustModuleType::LocalEnum(enum_symbol), &encoded_enum).unwrap(), enum_value);
            let scalar_value = ModuleValue::CustomScalar { ty: scalar_symbol.clone(), value: Box::new(ModuleValue::Integer(scalar)) };
            let encoded_scalar = codec.encode(&RustModuleType::CustomScalar(scalar_symbol.clone()), &scalar_value).unwrap();
            prop_assert_eq!(codec.decode(&RustModuleType::CustomScalar(scalar_symbol), &encoded_scalar).unwrap(), scalar_value);
        }

        // Recursive wrappers preserve null, list order, and explicit zero-like values without coercion.
        #[test]
        fn property_08_recursive_type_semantics_preserve_rust_distinctions(
            case in recursive_value_strategy(),
        ) {
            let (ty, value) = case;
            let catalog = TypeCatalog::default();
            let codec = ModuleValueCodec::new(&catalog);
            let encoded = codec.encode(&ty, &value).unwrap();
            prop_assert_eq!(codec.decode(&ty, &encoded).unwrap(), value);
            let wrong = match encoded {
                Value::Bool(_) => Value::String("false".to_owned()),
                _ => Value::Bool(true),
            };
            prop_assert!(codec.decode(&ty, &wrong).is_err());
        }
    }

    #[test]
    fn manifest_has_unique_complete_categories_and_no_fallback() {
        let manifest = rust_type_policy_manifest();
        assert_eq!(
            manifest.len(),
            manifest
                .iter()
                .map(|row| row.category)
                .collect::<BTreeSet<_>>()
                .len()
        );
        assert!(
            manifest
                .iter()
                .all(|row| !row.rust_form.contains("serde_json"))
        );
    }

    #[test]
    fn catalog_compiles_normalized_objects_interfaces_enums_and_scalars() {
        let path = super::super::model::ModuleSourcePath::new("src/lib.rs").unwrap();
        let declarations = super::super::authoring::AuthoringParser::parse(
            &path,
            r#"
                #[dagger_sdk::object(root, default = true)]
                pub struct Root {
                    #[dagger(field, rename = "publicValue")]
                    value: Values<String>,
                    #[dagger(state)]
                    count: i64,
                    cache: ForeignImplementationDetail,
                }

                #[dagger_sdk::interface]
                pub trait Named { fn name(&self) -> String; }

                #[dagger_sdk::object]
                pub struct Child {}

                #[dagger_sdk::enum_type]
                pub enum Mood { Happy, Quiet }

                #[dagger_sdk::scalar]
                pub struct Email(String);
            "#,
        )
        .unwrap();
        let declarations = declarations
            .into_iter()
            .map(|declaration| (declaration.rust_symbol.clone(), declaration))
            .collect::<BTreeMap<_, _>>();
        let interface = symbol("crate::Named");
        let child = symbol("crate::Child");
        let discovery = ModuleDiscovery {
            root: symbol("crate::Root"),
            declarations,
            interface_implementations: BTreeMap::from([(
                interface.clone(),
                BTreeSet::from([child.clone()]),
            )]),
            type_aliases: BTreeMap::from([(
                "crate::Values".to_owned(),
                super::super::source::ResolvedTypeAlias {
                    rust_path: "crate::Values".to_owned(),
                    parameters: vec!["T".to_owned()],
                    target: "Vec<T>".to_owned(),
                },
            )]),
            generated_types: BTreeMap::new(),
            visited_documents: BTreeSet::from([path]),
            source_digest: super::super::model::Sha256Digest::hash_bytes(b"catalog fixture"),
        };
        let catalog = TypeCatalog::compile(&discovery).unwrap();
        let root = &catalog.objects[&symbol("crate::Root")];
        assert_eq!(root.fields.len(), 2);
        assert_eq!(
            root.fields[0].ty,
            RustModuleType::List(Box::new(RustModuleType::String))
        );
        assert_eq!(
            root.exposed_fields().next().unwrap().wire_name.as_str(),
            "publicValue"
        );
        assert_eq!(root.construction, Some(ConstructionPolicy::Default));
        assert_eq!(
            catalog.interfaces[&interface].implementations,
            BTreeSet::from([child])
        );
        assert_eq!(
            catalog.enums[&symbol("crate::Mood")]
                .wire_members()
                .unwrap()
                .len(),
            2
        );
        assert_eq!(
            catalog.scalars[&symbol("crate::Email")].representation,
            RustModuleType::String
        );
    }

    fn recursive_value_strategy() -> impl Strategy<Value = (RustModuleType, ModuleValue)> {
        let leaf = prop_oneof![
            any::<String>().prop_map(|value| (RustModuleType::String, ModuleValue::String(value))),
            any::<i64>().prop_map(|value| (RustModuleType::Integer, ModuleValue::Integer(value))),
            any::<bool>().prop_map(|value| (RustModuleType::Boolean, ModuleValue::Boolean(value))),
        ];
        leaf.prop_recursive(4, 64, 8, |inner| {
            prop_oneof![
                proptest::collection::vec(inner.clone(), 0..6).prop_map(|values| {
                    let element = values
                        .first()
                        .map(|(ty, _)| ty.clone())
                        .unwrap_or(RustModuleType::String);
                    let values = values
                        .into_iter()
                        .filter_map(|(ty, value)| (ty == element).then_some(value))
                        .collect();
                    (
                        RustModuleType::List(Box::new(element)),
                        ModuleValue::List(values),
                    )
                }),
                proptest::option::of(inner).prop_map(|value| match value {
                    Some((ty, value))
                        if !matches!(ty, RustModuleType::Optional(_) | RustModuleType::Void) =>
                        (
                            RustModuleType::Optional(Box::new(ty)),
                            ModuleValue::Optional(Some(Box::new(value)))
                        ),
                    _ => (
                        RustModuleType::Optional(Box::new(RustModuleType::String)),
                        ModuleValue::Optional(None)
                    ),
                }),
            ]
        })
    }

    fn symbol(value: &str) -> RustSymbol {
        RustSymbol::new(value).expect("fixture symbol is canonical")
    }
}
