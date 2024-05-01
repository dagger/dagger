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

        impl From<&str> for $(t.name.pipe(|n| format_name(n))) {
            fn from(value: &str) -> Self {
                Self(value.to_string())
            }
        }

        impl From<String> for $(t.name.pipe(|n| format_name(n))) {
            fn from(value: String) -> Self {
                Self(value)
            }
        }

        impl $(t.name.pipe(|n| format_name(n))) {
            fn quote(&self) -> String {
                format!(r#""{}""#, self.0.clone())
            }
        }
    })
}
