mod dagger_generated {
    pub mod __private {
        #[allow(unused_imports)]
        pub use dagger_sdk::__private::*;
    }
}

#[dagger_sdk::enum_type]
pub enum PayloadEnum {
    Unsupported(String),
}

fn main() {}
