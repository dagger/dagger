//! Static descriptor and registration views embedded in generated modules.
//!
//! These exact-version values let generated registries expose their closed dispatch
//! surface without reflection or runtime registration libraries. They contain no
//! authored values, session handles, or mutable process state.

/// Kind of a generated module type.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ModuleTypeKindView {
    /// Stateful object.
    Object,
    /// Closed interface.
    Interface,
    /// Unit enum.
    Enum,
    /// Transparent scalar.
    Scalar,
}

/// One generated local type coordinate.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ModuleTypeView {
    /// Exact target wire name.
    pub wire_name: &'static str,
    /// Descriptor type category.
    pub kind: ModuleTypeKindView,
}

/// One generated function argument coordinate.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ArgumentView {
    /// Exact target wire name.
    pub wire_name: &'static str,
    /// Whether omission is rejected before invocation.
    pub required: bool,
    /// Canonical JSON used when the argument is omitted, when one was declared.
    pub default_json: Option<&'static str>,
}

/// One callable generated descriptor entry.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct FunctionView {
    /// Parent target wire name.
    pub parent_wire_name: &'static str,
    /// Function target wire name.
    pub function_wire_name: &'static str,
    /// Whether the callable constructs the root without parent reconstruction.
    pub constructor: bool,
    /// Ordered data-argument coordinates.
    pub arguments: &'static [ArgumentView],
    /// Canonical successful result type retained for encoding diagnostics.
    pub result_type: &'static str,
}

/// Complete static descriptor view embedded in one generated module.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ModuleDescriptorView {
    /// Exact canonical descriptor digest.
    pub digest: &'static str,
    /// Root target wire name.
    pub root_wire_name: &'static str,
    /// Every generated local type in canonical order.
    pub types: &'static [ModuleTypeView],
    /// Every callable in canonical dispatch order.
    pub functions: &'static [FunctionView],
}

/// Complete registration value generated from the same descriptor.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct RegistrationView {
    /// Descriptor digest bound to the registration projection.
    pub descriptor_digest: &'static str,
    /// Canonical registration JSON consumed by the recording or engine adapter.
    pub canonical_json: &'static str,
}
