use crate::functions::FormatTypeFuncs;

use super::functions::format_name;

pub struct FormatTypeFunc;

impl FormatTypeFuncs for FormatTypeFunc {
    fn format_kind_list(&self, representation: &str, _input: bool, _immutable: bool) -> String {
        format!("Vec<{}>", representation)
    }

    fn format_kind_scalar_string(&self, representation: &str, input: bool) -> String {
        let mut rep = representation.to_string();
        if input {
            rep.push_str("impl Into<String>");
        } else {
            rep.push_str("String");
        }
        rep
    }

    fn format_kind_scalar_int(&self, representation: &str) -> String {
        let mut rep = representation.to_string();
        rep.push_str("isize");
        rep
    }

    fn format_kind_scalar_float(&self, representation: &str) -> String {
        let mut rep = representation.to_string();
        rep.push_str("float");
        rep
    }

    fn format_kind_scalar_boolean(&self, representation: &str) -> String {
        let mut rep = representation.to_string();
        rep.push_str("bool");
        rep
    }

    fn format_kind_scalar_default(
        &self,
        representation: &str,
        ref_name: &str,
        _input: bool,
    ) -> String {
        let mut rep = representation.to_string();
        rep.push_str(&format_name(ref_name));
        rep
    }

    fn format_kind_object(&self, representation: &str, ref_name: &str) -> String {
        let mut rep = representation.to_string();
        rep.push_str(&format_name(ref_name));
        rep
    }

    fn format_kind_input_object(&self, representation: &str, ref_name: &str) -> String {
        let mut rep = representation.to_string();
        rep.push_str(&format_name(ref_name));
        rep
    }

    fn format_kind_enum(&self, representation: &str, ref_name: &str) -> String {
        let mut rep = representation.to_string();
        rep.push_str(&format_name(ref_name));
        rep
    }
}
