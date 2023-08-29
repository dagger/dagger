use dagger_sdk::core::introspection::FullType;
use genco::prelude::rust;
use genco::quote;

use crate::rust::functions::format_name;
use crate::utility::OptionExt;

pub fn render_scalar(t: &FullType) -> eyre::Result<rust::Tokens> {
    let deserialize = rust::import("serde", "Deserialize");
    let serialize = rust::import("serde", "Serialize");

    Ok(quote! {
        #[derive($serialize, $deserialize, PartialEq, Debug, Clone)]
        pub struct $(t.name.pipe(|n|format_name(n)))(pub String);

        impl Into<$(t.name.pipe(|n| format_name(n)))> for &str {
            fn into(self) -> $(t.name.pipe(|n| format_name(n))) {
                $(t.name.pipe(|n| format_name(n)))(self.to_string())
            }
        }

        impl Into<$(t.name.pipe(|n| format_name(n)))> for String {
            fn into(self) -> $(t.name.pipe(|n| format_name(n))) {
                $(t.name.pipe(|n| format_name(n)))(self.clone())
            }
        }

        impl $(t.name.pipe(|n| format_name(n))) {
            fn quote(&self) -> String {
                format!(r#""{}""#, self.0.clone())
            }
        }
    })
}
