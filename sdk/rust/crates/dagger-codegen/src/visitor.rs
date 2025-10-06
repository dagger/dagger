use std::sync::Arc;

use dagger_sdk::core::introspection::{FullType, Schema, __TypeKind};
use itertools::Itertools;

pub struct Visitor {
    pub schema: Schema,
    pub handlers: VisitHandlers,
}

pub type VisitFunc = Arc<dyn Fn(&FullType) -> eyre::Result<()>>;

pub struct VisitHandlers {
    pub visit_scalar: VisitFunc,
    pub visit_object: VisitFunc,
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
        let sequence = vec![
            SequenceItem {
                kind: __TypeKind::SCALAR,
                handler: self.handlers.visit_scalar.clone(),
                ignore: Some(vec![
                    "String".into(),
                    "Float".into(),
                    "Int".into(),
                    "Boolean".into(),
                    "DateTime".into(),
                    "ID".into(),
                ]),
            },
            SequenceItem {
                kind: __TypeKind::INPUT_OBJECT,
                handler: self.handlers.visit_input.clone(),
                ignore: None,
            },
            SequenceItem {
                kind: __TypeKind::OBJECT,
                handler: self.handlers.visit_object.clone(),
                ignore: None,
            },
            SequenceItem {
                kind: __TypeKind::ENUM,
                handler: self.handlers.visit_enum.clone(),
                ignore: None,
            },
        ];

        for item in sequence {
            self.visit(&item)?;
        }

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
