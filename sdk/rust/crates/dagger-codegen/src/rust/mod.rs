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
use self::templates::heading_tmpl::render_heading;
use self::templates::input_tmpl::render_input;
use self::templates::interface_tmpl::{render_interface, render_interface_impl_for_object};
use self::templates::object_tmpl::render_object;
use self::templates::scalar_tmpl::render_scalar;

pub struct RustGenerator {}

impl Generator for RustGenerator {
    fn generate(&self, schema: Schema) -> eyre::Result<String> {
        let render = Arc::new(Mutex::new(rust::Tokens::new()));
        let common_funcs = Arc::new(CommonFunctions::new(Arc::new(FormatTypeFunc {})));

        tracing::info!("generating dagger for rust");

        let visitor = Visitor {
            schema,
            handlers: VisitHandlers {
                visit_scalar: Arc::new({
                    let render = render.clone();

                    move |t| {
                        tracing::debug!("generating scalar");
                        let rendered_scalar = render_scalar(t)?;

                        let mut render = render.lock().unwrap();

                        render.append(rendered_scalar);
                        render.push();

                        tracing::debug!("generated scalar");

                        Ok(())
                    }
                }),
                visit_object: Arc::new({
                    let render = render.clone();
                    let common_funcs = common_funcs.clone();

                    move |t, interface_types| {
                        tracing::debug!("generating object");
                        let rendered_object = render_object(&common_funcs, t)?;

                        let mut render = render.lock().unwrap();
                        render.append(rendered_object);
                        render.push();

                        // Generate `impl Interface for Object` for each
                        // interface this object declares.
                        if let Some(ifaces) = &t.interfaces {
                            for iface_ref in ifaces {
                                if let Some(iface_name) = &iface_ref.type_ref.name {
                                    if let Some(iface_type) = interface_types
                                        .iter()
                                        .find(|it| it.name.as_deref() == Some(iface_name))
                                    {
                                        let impl_tokens = render_interface_impl_for_object(
                                            &common_funcs,
                                            t,
                                            iface_type,
                                        );
                                        render.append(impl_tokens);
                                        render.push();
                                    }
                                }
                            }
                        }

                        tracing::debug!("generated object");

                        Ok(())
                    }
                }),
                visit_interface: Arc::new({
                    let render = render.clone();
                    let common_funcs = common_funcs.clone();

                    move |t| {
                        tracing::debug!("generating interface");
                        let rendered = render_interface(&common_funcs, t)?;

                        let mut render = render.lock().unwrap();
                        render.append(rendered);
                        render.push();
                        tracing::debug!("generated interface");

                        Ok(())
                    }
                }),
                visit_input: Arc::new({
                    let render = render.clone();
                    let common_funcs = common_funcs.clone();

                    move |t| {
                        tracing::debug!("generating input");
                        let rendered_scalar = render_input(&common_funcs, t)?;

                        let mut render = render.lock().unwrap();

                        render.append(rendered_scalar);
                        render.push();
                        tracing::debug!("generated input");

                        Ok(())
                    }
                }),
                visit_enum: Arc::new({
                    let render = render.clone();
                    let _common_funcs = common_funcs.clone();

                    move |t| {
                        tracing::debug!("generating enum");
                        let rendered_scalar = render_enum(t)?;

                        let mut render = render.lock().unwrap();

                        render.append(rendered_scalar);
                        render.push();
                        tracing::debug!("generated enum");

                        Ok(())
                    }
                }),
            },
        };

        visitor.run()?;

        tracing::info!("done generating objects");

        let rendered = render.lock().unwrap();

        let body = rendered
            .to_file_string()
            .context("could not render to file string")?;

        let heading = render_heading()
            .to_string()
            .context("failed to render heaing")?;

        Ok(format!("{heading}\n\n{body}"))
    }
}
