use std::sync::Arc;

use dagger_sdk::core::introspection::Schema;

pub trait Generator {
    fn generate(&self, schema: Schema) -> eyre::Result<String>;
}

pub type DynGenerator = Arc<dyn Generator + Send + Sync>;

pub trait FormatTypeRefs {
    fn format_kind_list(representation: &str) -> String;
    fn format_kind_scalar_string(representation: &str) -> String;
    fn format_kind_scalar_int(representation: &str) -> String;
    fn format_kind_scalar_float(representation: &str) -> String;
    fn format_kind_scalar_boolean(representation: &str) -> String;
    fn format_kind_scalar_default(representation: &str, ref_name: &str, input: bool) -> String;
    fn format_kind_object(representation: &str, ref_name: &str) -> String;
    fn format_kind_input_object(representation: &str, ref_name: &str) -> String;
    fn format_kind_enum(representation: &str, ref_name: &str) -> String;
}
