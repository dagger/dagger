use std::sync::Arc;

use dagger_sdk::core::introspection::{FullType, Schema, __TypeKind};
use itertools::Itertools;

pub struct Visitor {
    pub schema: Schema,
    pub handlers: VisitHandlers,
}

pub type VisitFunc = Arc<dyn Fn(&FullType) -> eyre::Result<()>>;

/// Callback for visiting object types. Receives the object and the full
/// list of interface types so it can generate trait impls.
pub type VisitObjectFunc = Arc<dyn Fn(&FullType, &[FullType]) -> eyre::Result<()>>;

pub struct VisitHandlers {
    pub visit_scalar: VisitFunc,
    pub visit_object: VisitObjectFunc,
    pub visit_interface: VisitFunc,
    pub visit_input: VisitFunc,
    pub visit_enum: VisitFunc,
}

struct SequenceItem {
    kind: __TypeKind,
    handler: VisitFunc,
    ignore: Option<Vec<String>>,
}

impl Visitor {
    pub fn run(&self) -> eyre::Result<()> {
        // Collect all interface FullTypes for passing to the object handler.
        let interface_types = self.collect_types(__TypeKind::INTERFACE);

        // Phase 1: scalars, inputs, interfaces (traits + client structs)
        let phase1 = vec![
            SequenceItem {
                kind: __TypeKind::SCALAR,
                handler: self.handlers.visit_scalar.clone(),
                ignore: Some(vec![
                    "String".into(),
                    "Float".into(),
                    "Int".into(),
                    "Boolean".into(),
                    "DateTime".into(),
                ]),
            },
            SequenceItem {
                kind: __TypeKind::INPUT_OBJECT,
                handler: self.handlers.visit_input.clone(),
                ignore: None,
            },
            SequenceItem {
                kind: __TypeKind::INTERFACE,
                handler: self.handlers.visit_interface.clone(),
                ignore: None,
            },
        ];

        for item in phase1 {
            self.visit(&item)?;
        }

        // Phase 2: objects (need interface types for trait impl generation)
        self.visit_objects(&interface_types)?;

        // Phase 3: enums
        self.visit(&SequenceItem {
            kind: __TypeKind::ENUM,
            handler: self.handlers.visit_enum.clone(),
            ignore: None,
        })?;

        Ok(())
    }

    /// Collect all FullTypes of a given kind, filtering internal types.
    fn collect_types(&self, kind: __TypeKind) -> Vec<FullType> {
        self.schema
            .types
            .as_ref()
            .unwrap()
            .iter()
            .filter_map(|t| t.as_ref())
            .filter(|t| {
                t.full_type.kind.as_ref() == Some(&kind)
                    && t.full_type
                        .name
                        .as_ref()
                        .map(|n| !n.starts_with('_'))
                        .unwrap_or(false)
            })
            .map(|t| t.full_type.clone())
            .collect()
    }

    /// Visit objects, passing the collected interface types.
    fn visit_objects(&self, interface_types: &[FullType]) -> eyre::Result<()> {
        self.schema
            .types
            .as_ref()
            .unwrap()
            .iter()
            .filter_map(|t| t.as_ref())
            .filter(|t| {
                t.full_type.kind.as_ref() == Some(&__TypeKind::OBJECT)
                    && t.full_type
                        .name
                        .as_ref()
                        .map(|n| !n.starts_with('_'))
                        .unwrap_or(false)
            })
            .sorted_by(|a, b| {
                a.full_type
                    .name
                    .as_ref()
                    .unwrap()
                    .cmp(b.full_type.name.as_ref().unwrap())
            })
            .try_for_each(|t| (*self.handlers.visit_object)(&t.full_type, interface_types))?;

        Ok(())
    }

    fn visit(&self, item: &SequenceItem) -> eyre::Result<()> {
        self.schema
            .types
            .as_ref()
            .unwrap()
            .iter()
            .map(|t| t.as_ref().unwrap())
            .filter(|t| match t.full_type.kind.as_ref().unwrap() == &item.kind {
                true => match (item.ignore.as_ref(), t.full_type.name.as_ref()) {
                    (Some(ignore), Some(name)) => {
                        if name.starts_with("_") {
                            return false;
                        }
                        if ignore.contains(name) {
                            return false;
                        }

                        true
                    }
                    (None, Some(name)) => {
                        if name.starts_with("_") {
                            return false;
                        }
                        true
                    }
                    _ => false,
                },
                false => false,
            })
            .sorted_by(|a, b| {
                a.full_type
                    .name
                    .as_ref()
                    .unwrap()
                    .cmp(b.full_type.name.as_ref().unwrap())
            })
            .map(|t| (*item.handler)(&t.full_type))
            .collect::<eyre::Result<Vec<_>>>()?;

        Ok(())
    }
}
