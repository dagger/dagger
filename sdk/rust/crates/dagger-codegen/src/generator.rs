use std::sync::Arc;

use dagger_sdk::core::introspection::Schema;

pub trait Generator {
    fn generate(&self, schema: Schema) -> eyre::Result<String>;
}

pub type DynGenerator = Arc<dyn Generator + Send + Sync>;
