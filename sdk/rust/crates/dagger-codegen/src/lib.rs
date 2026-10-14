#![deny(warnings)]

mod functions;
mod generator;
pub mod naming;
pub mod rust;
pub mod utility;
mod visitor;

use dagger_sdk::core::introspection::Schema;

use self::generator::DynGenerator;

fn set_schema_parents(mut schema: Schema) -> Schema {
    for t in schema.types.as_mut().into_iter().flatten().flatten() {
        let t_parent = t.full_type.clone();
        for field in t.full_type.fields.as_mut().into_iter().flatten() {
            field.parent_type = Some(t_parent.clone());
        }
    }

    schema
}

pub fn generate(schema: Schema, generator: DynGenerator) -> eyre::Result<String> {
    let schema = set_schema_parents(schema);
    generator.generate(schema)
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use dagger_sdk::core::introspection::IntrospectionResponse;

    use super::generate;
    use crate::naming::NamesFile;
    use crate::rust::RustGenerator;

    fn generate_from_json(json: &str) -> String {
        generate_from_json_with_names(json, NamesFile::new())
    }

    fn generate_from_json_with_names(json: &str, names: NamesFile) -> String {
        let schema = serde_json::from_str::<IntrospectionResponse>(json).unwrap();
        generate(
            schema.into_schema().schema.unwrap(),
            Arc::new(RustGenerator::with_names(names)),
        )
        .unwrap()
    }

    fn generate_from_json_at_version(json: &str, schema_version: &str) -> String {
        let json = json.replacen(
            '{',
            &format!(r#"{{"__schemaVersion":"{schema_version}","#),
            1,
        );
        generate_from_json(&json)
    }

    /// Minimal schema with an interface, two implementing objects, and a
    /// Query root that returns the interface via node(id:).
    fn interface_schema() -> &'static str {
        r#"{
  "__schema": {
    "queryType": {"name": "Query"},
    "mutationType": null,
    "subscriptionType": null,
    "types": [
      {
        "kind": "SCALAR", "name": "ID", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "String", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "Boolean", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "Int", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "INTERFACE", "name": "Node",
        "description": "An object with a globally unique ID.",
        "fields": [
          {
            "name": "id", "description": "The unique ID.",
            "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          },
          {
            "name": "lookup", "description": "Lookup by path.",
            "args": [{
              "name": "path", "description": null,
              "type": {"kind": "NON_NULL", "name": null,
                "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
              "defaultValue": null
            }],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          }
        ],
        "inputFields": null, "interfaces": null, "enumValues": null,
        "possibleTypes": [
          {"kind": "OBJECT", "name": "Container", "ofType": null},
          {"kind": "OBJECT", "name": "Directory", "ofType": null}
        ]
      },
      {
        "kind": "OBJECT", "name": "Container",
        "description": "A container.",
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          },
          {
            "name": "lookup", "description": "Lookup by path.",
            "args": [{
              "name": "path", "description": null,
              "type": {"kind": "NON_NULL", "name": null,
                "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
              "defaultValue": null
            }],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          },
          {
            "name": "imageRef", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          }
        ],
        "inputFields": null,
        "interfaces": [{"kind": "INTERFACE", "name": "Node", "ofType": null}],
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "Directory",
        "description": "A directory.",
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          },
          {
            "name": "lookup", "description": "Lookup by path.",
            "args": [{
              "name": "path", "description": null,
              "type": {"kind": "NON_NULL", "name": null,
                "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
              "defaultValue": null
            }],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          }
        ],
        "inputFields": null,
        "interfaces": [{"kind": "INTERFACE", "name": "Node", "ofType": null}],
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "Query",
        "description": null,
        "fields": [
          {
            "name": "node", "description": null,
            "args": [{
              "name": "id", "description": null,
              "type": {"kind": "NON_NULL", "name": null,
                "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
              "defaultValue": null
            }],
            "type": {"kind": "INTERFACE", "name": "Node", "ofType": null},
            "isDeprecated": false, "deprecationReason": null
          }
        ],
        "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      }
    ],
    "directives": []
  }
}"#
    }

    #[test]
    fn interface_generates_trait() {
        let code = generate_from_json(interface_schema());
        // Should produce a trait, not just a struct
        assert!(
            code.contains("pub trait Node"),
            "expected 'pub trait Node' in generated code"
        );
    }

    #[test]
    fn interface_generates_client_struct() {
        let code = generate_from_json(interface_schema());
        // The concrete struct for the interface is named FooClient
        assert!(
            code.contains("pub struct NodeClient"),
            "expected 'pub struct NodeClient' in generated code"
        );
        // No bare `struct Node` (that would collide with the trait)
        assert!(
            !code.contains("pub struct Node {"),
            "should not generate 'pub struct Node' (collides with trait)"
        );
    }

    #[test]
    fn interface_trait_impl_on_client() {
        let code = generate_from_json(interface_schema());
        assert!(
            code.contains("impl Node for NodeClient"),
            "expected 'impl Node for NodeClient'"
        );
    }

    #[test]
    fn interface_trait_impl_on_objects() {
        let code = generate_from_json(interface_schema());
        assert!(
            code.contains("impl Node for Container"),
            "expected 'impl Node for Container'"
        );
        assert!(
            code.contains("impl Node for Directory"),
            "expected 'impl Node for Directory'"
        );
    }

    #[test]
    fn interface_trait_impl_required_string_args_are_converted() {
        let code = generate_from_json(interface_schema());
        assert!(
            code.contains(r#"query = query.arg("path", path.into());"#),
            "trait impl should convert impl Into<String> before serializing it, got:\n{}",
            code.lines()
                .filter(|l| l.contains("path") || l.contains("lookup"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        assert!(
            !code.contains(r#"query = query.arg("path", path);"#),
            "trait impl must not pass impl Into<String> directly to Selection::arg"
        );
    }

    #[test]
    fn interface_return_type_uses_client() {
        let code = generate_from_json(interface_schema());
        // Query.node() should return an optional NodeClient, not Node.
        assert!(
            code.contains("Result<Option<NodeClient>, DaggerError>"),
            "expected node() to return an optional NodeClient, not Node"
        );
    }

    #[test]
    fn nullable_interface_keeps_older_engine_shape() {
        let code = generate_from_json_at_version(interface_schema(), "v1.0.0-beta.9");
        assert!(
            code.contains("pub fn node(") && code.contains("-> NodeClient"),
            "expected node() to remain chainable for older engines, got:\n{}",
            code.lines()
                .filter(|line| line.contains("fn node"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        assert!(!code.contains("Result<Option<NodeClient>, DaggerError>"));
    }

    #[test]
    fn loadable_impl_on_objects() {
        let code = generate_from_json(interface_schema());
        assert!(
            code.contains("impl Loadable for Container"),
            "expected 'impl Loadable for Container'"
        );
        assert!(
            code.contains("impl Loadable for Directory"),
            "expected 'impl Loadable for Directory'"
        );
    }

    #[test]
    fn loadable_impl_on_interface_client() {
        let code = generate_from_json(interface_schema());
        assert!(
            code.contains("impl Loadable for NodeClient"),
            "expected 'impl Loadable for NodeClient'"
        );
        // The GraphQL name must be the interface name, not the Rust struct name.
        assert!(
            code.contains(r#""Node""#),
            "NodeClient.graphql_type() should return \"Node\", not \"NodeClient\""
        );
    }

    #[test]
    fn no_loadable_on_query() {
        let code = generate_from_json(interface_schema());
        assert!(
            !code.contains("impl Loadable for Query"),
            "Query should not implement Loadable (no id field)"
        );
    }

    /// Schema with `@expectedType` directives on field returns and arguments.
    fn expected_type_schema() -> &'static str {
        r#"{
  "__schema": {
    "queryType": {"name": "Query"},
    "mutationType": null,
    "subscriptionType": null,
    "types": [
      {
        "kind": "SCALAR", "name": "ID", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "String", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "Boolean", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "Int", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "Container",
        "description": "A container.",
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Container\""}]}]
          },
          {
            "name": "sync", "description": "Force evaluation.", "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Container\""}]}]
          },
          {
            "name": "withDirectory", "description": "Add a directory.",
            "args": [
              {
                "name": "path", "description": null,
                "type": {"kind": "NON_NULL", "name": null,
                  "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
                "defaultValue": null
              },
              {
                "name": "directory", "description": null,
                "type": {"kind": "NON_NULL", "name": null,
                  "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
                "defaultValue": null,
                "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Directory\""}]}]
              }
            ],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "OBJECT", "name": "Container", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          },
          {
            "name": "imageRef", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          }
        ],
        "inputFields": null,
        "interfaces": [],
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "Directory",
        "description": "A directory.",
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Directory\""}]}]
          }
        ],
        "inputFields": null,
        "interfaces": [],
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "Query",
        "description": null,
        "fields": [
          {
            "name": "container", "description": null,
            "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "OBJECT", "name": "Container", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          }
        ],
        "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      }
    ],
    "directives": []
  }
}"#
    }

    /// Minimal schema whose ID-returning fields name another object
    /// (`LLM.spawn` -> `Agent`) and an interface (`LLM.syncer` -> `Syncer`),
    /// plus a `File` implementing `Syncer`, whose `sync` names the interface.
    fn id_handle_schema() -> &'static str {
        r#"{
  "__schema": {
    "queryType": {"name": "Query"},
    "mutationType": null,
    "subscriptionType": null,
    "types": [
      {
        "kind": "SCALAR", "name": "ID", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "INTERFACE", "name": "Syncer", "description": null,
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Syncer\""}]}]
          },
          {
            "name": "sync", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Syncer\""}]}]
          }
        ],
        "inputFields": null, "interfaces": null,
        "enumValues": null,
        "possibleTypes": [{"kind": "OBJECT", "name": "File", "ofType": null}]
      },
      {
        "kind": "OBJECT", "name": "File", "description": null,
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"File\""}]}]
          },
          {
            "name": "sync", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"File\""}]}]
          }
        ],
        "inputFields": null,
        "interfaces": [{"kind": "INTERFACE", "name": "Syncer", "ofType": null}],
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "Agent", "description": null,
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Agent\""}]}]
          }
        ],
        "inputFields": null, "interfaces": [],
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "LLM", "description": null,
        "fields": [
          {
            "name": "id", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"LLM\""}]}]
          },
          {
            "name": "spawn", "description": "Spawn an agent.", "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Agent\""}]}]
          },
          {
            "name": "syncer", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "ID", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null,
            "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"Syncer\""}]}]
          }
        ],
        "inputFields": null, "interfaces": [],
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "OBJECT", "name": "Query", "description": null,
        "fields": [
          {
            "name": "llm", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "OBJECT", "name": "LLM", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null, "directives": []
          }
        ],
        "inputFields": null, "interfaces": [],
        "enumValues": null, "possibleTypes": null
      }
    ],
    "directives": []
  }
}"#
    }

    #[test]
    fn id_handles_load_their_expected_type() {
        let code = generate_from_json(id_handle_schema());
        // another object's ID loads that object
        assert!(
            code.contains("fn spawn") && code.contains("-> Result<Agent, DaggerError>"),
            "spawn should return Agent:\n{}",
            code.lines()
                .filter(|l| l.contains("spawn"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        assert!(code.contains("inline_fragment(\"Agent\")"));
        // an interface's ID loads through the interface's client struct
        assert!(
            code.contains("fn syncer") && code.contains("-> Result<SyncerClient, DaggerError>"),
            "syncer should return SyncerClient:\n{}",
            code.lines()
                .filter(|l| l.contains("syncer"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        assert!(code.contains("inline_fragment(\"Syncer\")"));
    }

    /// The body of the `impl <Trait> for <Type>` block that starts with
    /// `header`, up to the next `impl` block.
    fn impl_block<'a>(code: &'a str, header: &str) -> &'a str {
        let start = code
            .find(header)
            .unwrap_or_else(|| panic!("missing {header}"));
        let rest = &code[start + header.len()..];
        let end = rest
            .match_indices("impl ")
            .map(|(i, _)| i)
            .find(|&i| rest[i + 5..].starts_with(|c: char| c.is_ascii_uppercase()))
            .unwrap_or(rest.len());
        &rest[..end]
    }

    #[test]
    fn interface_self_handles_return_the_implementing_type() {
        let code = generate_from_json(id_handle_schema());
        // the trait declares the interface's own handle as Self
        assert!(
            code.contains("fn sync(&self) -> impl core::future::Future<Output = Result<Self, DaggerError>> + Send where Self: Sized;"),
            "trait should return Self:\n{}",
            code.lines()
                .filter(|l| l.contains("fn sync"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        // each implementation loads its own type
        let file_impl = impl_block(&code, "impl Syncer for File");
        assert!(file_impl.contains("Ok(Self {"), "File impl:\n{file_impl}");
        assert!(
            file_impl.contains("inline_fragment(\"File\")"),
            "File impl:\n{file_impl}"
        );
        let client_impl = impl_block(&code, "impl Syncer for SyncerClient");
        assert!(
            client_impl.contains("Ok(Self {"),
            "client impl:\n{client_impl}"
        );
        assert!(
            client_impl.contains("inline_fragment(\"Syncer\")"),
            "client impl:\n{client_impl}"
        );
        // and the inherent method on the object still returns the object
        assert!(code.contains("-> Result<File, DaggerError>"));
    }

    #[test]
    fn convert_id_sync_returns_parent() {
        let code = generate_from_json(expected_type_schema());
        // sync() should return Result<Container, DaggerError>, not Result<Id, DaggerError>
        assert!(
            code.contains("fn sync") && code.contains("-> Result<Container, DaggerError>"),
            "sync() should return Container, got:\n{}",
            code.lines()
                .filter(|l| l.contains("sync"))
                .collect::<Vec<_>>()
                .join("\n")
        );
    }

    #[test]
    fn convert_id_sync_uses_node_reload() {
        let code = generate_from_json(expected_type_schema());
        // sync() body should use node(id) + inline_fragment to reconstruct
        assert!(
            code.contains("select(\"node\")"),
            "sync() should reconstruct via node(), got:\n{}",
            code.lines()
                .filter(|l| l.contains("node") || l.contains("sync"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        assert!(
            code.contains("inline_fragment(\"Container\")"),
            "sync() should use inline_fragment(\"Container\")\n{}",
            code.lines()
                .filter(|l| l.contains("inline_fragment"))
                .collect::<Vec<_>>()
                .join("\n")
        );
    }

    #[test]
    fn id_field_not_converted() {
        let code = generate_from_json(expected_type_schema());
        // id() should still return Result<Id, DaggerError>, not Result<Container, DaggerError>
        assert!(
            code.contains("fn id") && code.contains("-> Result<Id, DaggerError>"),
            "id() should return Id, got:\n{}",
            code.lines()
                .filter(|l| l.contains("fn id"))
                .collect::<Vec<_>>()
                .join("\n")
        );
    }

    #[test]
    fn expected_type_arg_accepts_object() {
        let code = generate_from_json(expected_type_schema());
        // withDirectory's directory arg should use IntoID<Id> (accepting Directory objects)
        assert!(
            code.contains("directory: impl IntoID<Id>"),
            "directory arg should accept objects via IntoID<Id>, got:\n{}",
            code.lines()
                .filter(|l| l.contains("with_directory"))
                .collect::<Vec<_>>()
                .join("\n")
        );
    }

    /// Schema with optional enum and string args. Enum names can contain `str`
    /// without borrowing anything, e.g. `RegistryProtocol`.
    fn optional_arg_lifetime_schema() -> &'static str {
        r#"{
  "__schema": {
    "queryType": {"name": "Query"},
    "mutationType": null,
    "subscriptionType": null,
    "types": [
      {
        "kind": "SCALAR", "name": "ID", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "String", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "Boolean", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "SCALAR", "name": "Int", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      },
      {
        "kind": "ENUM", "name": "RegistryProtocol", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "possibleTypes": null,
        "enumValues": [
          {"name": "HTTPS", "description": null, "isDeprecated": false, "deprecationReason": null},
          {"name": "HTTP", "description": null, "isDeprecated": false, "deprecationReason": null}
        ]
      },
      {
        "kind": "OBJECT", "name": "Query",
        "description": null,
        "fields": [
          {
            "name": "enumOption", "description": null,
            "args": [{
              "name": "protocol", "description": null,
              "type": {"kind": "ENUM", "name": "RegistryProtocol", "ofType": null},
              "defaultValue": null
            }],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          },
          {
            "name": "stringOption", "description": null,
            "args": [{
              "name": "name", "description": null,
              "type": {"kind": "SCALAR", "name": "String", "ofType": null},
              "defaultValue": null
            }],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          }
        ],
        "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      }
    ],
    "directives": []
  }
}"#
    }

    #[test]
    fn optional_enum_arg_does_not_add_lifetime() {
        let code = generate_from_json(optional_arg_lifetime_schema());

        assert!(
            code.contains("pub enum RegistryProtocol"),
            "expected RegistryProtocol enum in generated code"
        );
        assert!(
            code.contains("pub struct QueryEnumOptionOpts {"),
            "optional enum args should not add a lifetime, got:\n{}",
            code.lines()
                .filter(|l| l.contains("QueryEnumOptionOpts"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        assert!(
            !code.contains("pub struct QueryEnumOptionOpts<'a>"),
            "RegistryProtocol contains `str` in its name but does not borrow"
        );
        assert!(
            code.contains("pub protocol: Option<RegistryProtocol>,"),
            "expected optional enum field to use RegistryProtocol"
        );
    }

    #[test]
    fn optional_string_arg_still_adds_lifetime() {
        let code = generate_from_json(optional_arg_lifetime_schema());

        assert!(
            code.contains("pub struct QueryStringOptionOpts<'a>"),
            "optional string args should still add a lifetime, got:\n{}",
            code.lines()
                .filter(|l| l.contains("QueryStringOptionOpts"))
                .collect::<Vec<_>>()
                .join("\n")
        );
        assert!(
            code.contains("pub name: Option<&'a str>,"),
            "expected optional string field to borrow with &'a str"
        );
    }

    /// The names of [`engine_names_schema`] as the engine formats them, in the
    /// formats Rust codegen uses: a names file.
    fn engine_names() -> NamesFile {
        let pascal = [
            ("ID", "Id"),
            ("Query", "Query"),
            ("JSONValue", "JsonValue"),
            ("jsonValue", "JsonValue"),
            ("prerequisiteSHAs", "PrerequisiteShas"),
            ("experimentalWithAllGPUs", "ExperimentalWithAllGpus"),
            ("insecureSkipTLSVerify", "InsecureSkipTlsVerify"),
            ("LLMInput", "LlmInput"),
            ("ImageMediaTypes", "ImageMediaTypes"),
            ("OCI", "Oci"),
            ("ref", "Ref"),
            ("formatIdentifiers", "FormatIdentifiers"),
        ];
        let snake = [
            ("ID", "id"),
            ("Query", "query"),
            ("JSONValue", "json_value"),
            ("jsonValue", "json_value"),
            ("prerequisiteSHAs", "prerequisite_shas"),
            ("experimentalWithAllGPUs", "experimental_with_all_gpus"),
            ("insecureSkipTLSVerify", "insecure_skip_tls_verify"),
            ("LLMInput", "llm_input"),
            ("ImageMediaTypes", "image_media_types"),
            ("OCI", "oci"),
            ("ref", "ref"),
            ("formatIdentifiers", "format_identifiers"),
        ];
        let map = |names: &[(&str, &str)]| {
            names
                .iter()
                .map(|(name, formatted)| (name.to_string(), formatted.to_string()))
                .collect()
        };
        [
            ("PASCAL:CAPITALIZED".to_string(), map(&pascal)),
            ("SNAKE:UPPERCASE".to_string(), map(&snake)),
        ]
        .into_iter()
        .collect()
    }

    /// Schema whose names the guessing converter splits badly. With
    /// `format_identifiers`, its Query has `formatIdentifiers`, the gate for
    /// engine-formatted names.
    fn engine_names_schema(format_identifiers: bool) -> String {
        let query_fields = if format_identifiers {
            r#",
          {
            "name": "formatIdentifiers", "description": null, "args": [],
            "type": {"kind": "NON_NULL", "name": null,
              "ofType": {"kind": "SCALAR", "name": "String", "ofType": null}},
            "isDeprecated": false, "deprecationReason": null
          }"#
        } else {
            ""
        };

        format!(
            r#"{{
  "__schema": {{
    "queryType": {{"name": "Query"}},
    "mutationType": null,
    "subscriptionType": null,
    "types": [
      {{
        "kind": "SCALAR", "name": "ID", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      }},
      {{
        "kind": "SCALAR", "name": "String", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      }},
      {{
        "kind": "SCALAR", "name": "Boolean", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": null, "possibleTypes": null
      }},
      {{
        "kind": "ENUM", "name": "ImageMediaTypes", "description": null,
        "fields": null, "inputFields": null, "interfaces": null,
        "possibleTypes": null,
        "enumValues": [
          {{"name": "OCI", "description": null, "isDeprecated": false, "deprecationReason": null}}
        ]
      }},
      {{
        "kind": "INPUT_OBJECT", "name": "LLMInput", "description": null,
        "fields": null, "interfaces": null, "enumValues": null, "possibleTypes": null,
        "inputFields": [
          {{
            "name": "prerequisiteSHAs", "description": null,
            "type": {{"kind": "NON_NULL", "name": null,
              "ofType": {{"kind": "SCALAR", "name": "String", "ofType": null}}}},
            "defaultValue": null
          }},
          {{
            "name": "ref", "description": null,
            "type": {{"kind": "NON_NULL", "name": null,
              "ofType": {{"kind": "SCALAR", "name": "String", "ofType": null}}}},
            "defaultValue": null
          }}
        ]
      }},
      {{
        "kind": "OBJECT", "name": "JSONValue", "description": null,
        "fields": [
          {{
            "name": "id", "description": null, "args": [],
            "type": {{"kind": "NON_NULL", "name": null,
              "ofType": {{"kind": "SCALAR", "name": "ID", "ofType": null}}}},
            "isDeprecated": false, "deprecationReason": null
          }},
          {{
            "name": "prerequisiteSHAs", "description": null, "args": [],
            "type": {{"kind": "NON_NULL", "name": null,
              "ofType": {{"kind": "SCALAR", "name": "String", "ofType": null}}}},
            "isDeprecated": false, "deprecationReason": null
          }},
          {{
            "name": "experimentalWithAllGPUs", "description": null,
            "args": [
              {{
                "name": "prerequisiteSHAs", "description": null,
                "type": {{"kind": "NON_NULL", "name": null,
                  "ofType": {{"kind": "SCALAR", "name": "String", "ofType": null}}}},
                "defaultValue": null
              }},
              {{
                "name": "insecureSkipTLSVerify", "description": null,
                "type": {{"kind": "SCALAR", "name": "Boolean", "ofType": null}},
                "defaultValue": null
              }}
            ],
            "type": {{"kind": "NON_NULL", "name": null,
              "ofType": {{"kind": "OBJECT", "name": "JSONValue", "ofType": null}}}},
            "isDeprecated": false, "deprecationReason": null
          }}
        ],
        "inputFields": null, "interfaces": [],
        "enumValues": null, "possibleTypes": null
      }},
      {{
        "kind": "OBJECT", "name": "Query", "description": null,
        "fields": [
          {{
            "name": "jsonValue", "description": null, "args": [],
            "type": {{"kind": "NON_NULL", "name": null,
              "ofType": {{"kind": "OBJECT", "name": "JSONValue", "ofType": null}}}},
            "isDeprecated": false, "deprecationReason": null
          }}{query_fields}
        ],
        "inputFields": null, "interfaces": [],
        "enumValues": null, "possibleTypes": null
      }}
    ],
    "directives": []
  }}
}}"#
        )
    }

    fn lines_with(code: &str, needle: &str) -> String {
        code.lines()
            .filter(|l| l.contains(needle))
            .collect::<Vec<_>>()
            .join("\n")
    }

    /// Whether `code` contains `needle`, ignoring whitespace: the generated
    /// code is only formatted by rustfmt later.
    fn has(code: &str, needle: &str) -> bool {
        fn squash(s: &str) -> String {
            s.chars().filter(|c| !c.is_whitespace()).collect()
        }
        squash(code).contains(&squash(needle))
    }

    fn generate_with_engine_names() -> String {
        generate_from_json_with_names(&engine_names_schema(true), engine_names())
    }

    #[test]
    fn engine_names_name_rust_identifiers() {
        let code = generate_with_engine_names();

        // Types: PascalCase with capitalized acronyms.
        assert!(has(&code, "pub struct JsonValue {"), "{code}");
        assert!(has(&code, "pub struct LlmInput {"), "{code}");
        assert!(has(&code, "pub enum ImageMediaTypes {"), "{code}");
        assert!(has(&code, "Oci,"), "{}", lines_with(&code, "Oci"));
        // Methods and arguments: snake_case, as the engine formats them.
        assert!(
            has(&code, "pub async fn prerequisite_shas(&self"),
            "{}",
            lines_with(&code, "prerequisite")
        );
        assert!(
            has(&code, "pub fn experimental_with_all_gpus(&self"),
            "{}",
            lines_with(&code, "experimental")
        );
        assert!(
            has(&code, "prerequisite_shas: impl Into<String>"),
            "{}",
            lines_with(&code, "prerequisite")
        );
        // Options structs and their fields.
        assert!(
            has(&code, "pub struct JsonValueExperimentalWithAllGpusOpts {"),
            "{}",
            lines_with(&code, "Opts")
        );
        assert!(
            has(&code, "pub insecure_skip_tls_verify: Option<bool>,"),
            "{}",
            lines_with(&code, "insecure")
        );
    }

    #[test]
    fn engine_names_keep_wire_names() {
        let code = generate_with_engine_names();

        // Selections and arguments use the schema's names.
        assert!(has(&code, r#"self.selection.select("prerequisiteSHAs")"#));
        assert!(has(
            &code,
            r#"self.selection.select("experimentalWithAllGPUs")"#
        ));
        assert!(has(
            &code,
            r#"query.arg("prerequisiteSHAs", prerequisite_shas.into())"#
        ));
        assert!(has(
            &code,
            r#"query.arg("insecureSkipTLSVerify", insecure_skip_tls_verify)"#
        ));
        assert!(has(
            &code,
            r#"fn graphql_type() -> &'static str { "JSONValue" }"#
        ));
        // Enum variants serialize as the schema's values.
        assert!(
            has(&code, r#"#[serde(rename = "OCI")] Oci,"#),
            "{}",
            lines_with(&code, "OCI")
        );
        // Input object fields keep the wire name serde derived before.
        assert!(
            has(
                &code,
                r#"#[serde(rename = "prerequisite_sh_as")] pub prerequisite_shas: String,"#
            ),
            "{}",
            lines_with(&code, "prerequisite")
        );
        // Unchanged names get no rename, and keywords stay escaped.
        assert!(has(&code, "pub r#ref: String,"));
        assert!(!has(&code, r#"#[serde(rename = "ref")]"#));
    }

    #[test]
    fn engine_names_keep_old_names_as_deprecated_aliases() {
        let code = generate_with_engine_names();

        assert!(
            has(
                &code,
                r#"#[deprecated(note = "use prerequisite_shas")] pub async fn prerequisite_sh_as(&self"#
            ),
            "{}",
            lines_with(&code, "prerequisite")
        );
        assert!(has(&code, "self.prerequisite_shas().await"));
        assert!(
            has(
                &code,
                r#"#[deprecated(note = "use experimental_with_all_gpus")] pub fn experimental_with_all_gp_us(&self"#
            ),
            "{}",
            lines_with(&code, "experimental")
        );
        assert!(has(
            &code,
            "self.experimental_with_all_gpus(prerequisite_shas)"
        ));
        // Only async methods are awaited.
        assert!(!has(
            &code,
            "self.experimental_with_all_gpus(prerequisite_shas).await"
        ));
        assert!(
            has(
                &code,
                r#"#[deprecated(note = "use experimental_with_all_gpus")] pub fn experimental_with_all_gp_us_opts(&self"#
            ),
            "{}",
            lines_with(&code, "experimental")
        );
        assert!(has(
            &code,
            "self.experimental_with_all_gpus_opts(prerequisite_shas, opts)"
        ));
        assert!(
            has(
                &code,
                r#"#[deprecated(note = "use JsonValueExperimentalWithAllGpusOpts")] pub type JsonValueExperimentalWithAllGpUsOpts = JsonValueExperimentalWithAllGpusOpts;"#
            ),
            "{}",
            lines_with(&code, "Opts")
        );
        // Names that didn't change get no alias.
        assert_eq!(code.matches("fn json_value(").count(), 1);
    }

    /// Code generated with the legacy converter: no engine-formatted names,
    /// no aliases, no renames.
    fn assert_guessed_names(code: &str) {
        assert!(has(code, "pub async fn prerequisite_sh_as(&self"));
        assert!(has(code, "pub fn experimental_with_all_gp_us(&self"));
        assert!(has(
            code,
            "pub struct JsonValueExperimentalWithAllGpUsOpts {"
        ));
        assert!(has(code, "pub prerequisite_sh_as: String,"));
        assert!(!code.contains("deprecated"));
        assert!(!code.contains("serde(rename = \"prerequisite"));
        assert!(!code.contains("prerequisite_shas"));
        assert!(has(code, "pub struct JsonValue {"));
    }

    #[test]
    fn missing_engine_names_keep_guessed_names() {
        assert_guessed_names(&generate_from_json(&engine_names_schema(true)));
    }

    #[test]
    fn engine_names_need_format_identifiers() {
        // A schema without Query.formatIdentifiers keeps the legacy
        // converter, even with names.
        assert_guessed_names(&generate_from_json_with_names(
            &engine_names_schema(false),
            engine_names(),
        ));
    }
}
