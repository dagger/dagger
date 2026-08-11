mod dagger_generated {
    pub mod __private {
        #[allow(unused_imports)]
        pub use dagger_sdk::__private::*;
    }
}

#[dagger_sdk::object(root, root)]
pub struct DuplicateMetadata {
    value: String,
}

fn main() {}
