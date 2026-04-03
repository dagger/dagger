#![deny(warnings)]

mod functions;
mod generator;
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
    use crate::rust::RustGenerator;

    fn generate_from_json(json: &str) -> String {
        let schema = serde_json::from_str::<IntrospectionResponse>(json).unwrap();
        generate(
            schema.into_schema().schema.unwrap(),
            Arc::new(RustGenerator {}),
        )
        .unwrap()
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
    fn interface_return_type_uses_client() {
        let code = generate_from_json(interface_schema());
        // Query.node() should return NodeClient, not Node
        assert!(
            code.contains("-> NodeClient"),
            "expected node() to return NodeClient, not Node"
        );
    }
}
