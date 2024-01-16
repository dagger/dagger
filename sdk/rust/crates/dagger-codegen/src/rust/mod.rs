pub mod format;
mod functions;
pub mod templates;

use std::sync::{Arc, Mutex};

use dagger_sdk::core::introspection::Schema;
use eyre::Context;
use genco::prelude::rust;

use crate::functions::CommonFunctions;
use crate::generator::Generator;
use crate::visitor::{VisitHandlers, Visitor};

use self::format::FormatTypeFunc;
use self::templates::enum_tmpl::render_enum;
use self::templates::input_tmpl::render_input;
use self::templates::object_tmpl::render_object;
use self::templates::scalar_tmpl::render_scalar;

pub struct RustGenerator {}

impl Generator for RustGenerator {
    fn generate(&self, schema: Schema) -> eyre::Result<String> {
        let render = Arc::new(Mutex::new(rust::Tokens::new()));
        let common_funcs = Arc::new(CommonFunctions::new(Arc::new(FormatTypeFunc {})));
        println!("generating dagger for rust");

        let visitor = Visitor {
            schema,
            handlers: VisitHandlers {
                visit_scalar: Arc::new({
                    let render = render.clone();

                    move |t| {
                        println!("generating scalar");
                        let rendered_scalar = render_scalar(t)?;

                        let mut render = render.lock().unwrap();

                        render.append(rendered_scalar);
                        render.push();

                        println!("generated scalar");

                        Ok(())
                    }
                }),
                visit_object: Arc::new({
                    let render = render.clone();
                    let common_funcs = common_funcs.clone();

                    move |t| {
                        println!("generating object");
                        let rendered_scalar = render_object(&common_funcs, t)?;

                        let mut render = render.lock().unwrap();

                        render.append(rendered_scalar);
                        render.push();
                        println!("generated object");

                        Ok(())
                    }
                }),
                visit_input: Arc::new({
                    let render = render.clone();
                    let common_funcs = common_funcs.clone();

                    move |t| {
                        println!("generating input");
                        let rendered_scalar = render_input(&common_funcs, t)?;

                        let mut render = render.lock().unwrap();

                        render.append(rendered_scalar);
                        render.push();
                        println!("generated input");

                        Ok(())
                    }
                }),
                visit_enum: Arc::new({
                    let render = render.clone();
                    let _common_funcs = common_funcs.clone();

                    move |t| {
                        println!("generating enum");
                        let rendered_scalar = render_enum(t)?;

                        let mut render = render.lock().unwrap();

                        render.append(rendered_scalar);
                        render.push();
                        println!("generated enum");

                        Ok(())
                    }
                }),
            },
        };

        visitor.run()?;

        println!("done generating objects");

        let rendered = render.lock().unwrap();

        rendered
            .to_file_string()
            .context("could not render to file string")
    }
}
