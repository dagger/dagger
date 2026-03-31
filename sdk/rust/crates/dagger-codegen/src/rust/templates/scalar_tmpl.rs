use dagger_sdk::core::introspection::FullType;
use genco::prelude::rust;
use genco::quote;

use crate::rust::functions::format_name;
use crate::utility::OptionExt;

pub fn render_scalar(t: &FullType) -> eyre::Result<rust::Tokens> {
    let deserialize = rust::import("serde", "Deserialize");
    let serialize = rust::import("serde", "Serialize");
    let into_id = &rust::import("crate::id", "IntoID");

    let name = t.name.pipe(|n| format_name(n));
    let name = name.as_ref();

    if let Some(original_name) = &t.name {
        // Void represents "no value" — the API returns null for void operations.
        if original_name == "Void" {
            return Ok(quote! {
                #[derive($serialize, PartialEq, Debug, Clone)]
                pub struct Void(pub Option<String>);

                impl<'de> serde::Deserialize<'de> for Void {
                    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
                    where
                        D: serde::Deserializer<'de>,
                    {
                        let opt = Option::<String>::deserialize(deserializer)?;
                        Ok(Void(opt))
                    }
                }

                impl From<&str> for Void {
                    fn from(value: &str) -> Self {
                        Self(Some(value.to_string()))
                    }
                }

                impl From<String> for Void {
                    fn from(value: String) -> Self {
                        Self(Some(value))
                    }
                }

                impl Void {
                    fn quote(&self) -> String {
                        match &self.0 {
                            Some(s) => format!(r#""{}""#, s),
                            None => "null".to_string(),
                        }
                    }
                }
            });
        }

        if original_name.ends_with("ID") {
            let name_without_id = &name.expect("Name should be available")
                [..name.expect("Name should be available").len() - 2];

            return Ok(quote! {
                #[derive($serialize, $deserialize, PartialEq, Debug, Clone)]
                pub struct $(name)(pub String);

                impl From<&str> for $(name) {
                    fn from(value: &str) -> Self {
                        Self(value.to_string())
                    }
                }

                impl From<String> for $(name) {
                    fn from(value: String) -> Self {
                        Self(value)
                    }
                }

                impl $(into_id)<$(name)> for $(name_without_id) {
                    fn into_id(self) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<$(name), DaggerError>> + Send>> {
                        Box::pin(async move { self.id().await })
                    }
                }

                impl $(into_id)<$(name)> for $(name) {
                    fn into_id(self) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<$(name), DaggerError>> + Send>> {
                        Box::pin(async move { Ok::<$(name), DaggerError>(self) })
                    }
                }

                impl $(name) {
                    fn quote(&self) -> String {
                        format!(r#""{}""#, self.0.clone())
                    }
                }
            });
        }
    }

    Ok(quote! {
        #[derive($serialize, $deserialize, PartialEq, Debug, Clone)]
        pub struct $(name)(pub String);

        impl From<&str> for $(name) {
            fn from(value: &str) -> Self {
                Self(value.to_string())
            }
        }

        impl From<String> for $(name) {
            fn from(value: String) -> Self {
                Self(value)
            }
        }

        impl $(name) {
            fn quote(&self) -> String {
                format!(r#""{}""#, self.0.clone())
            }
        }
    })
}
