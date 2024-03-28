//#![deny(warnings)]

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
