mod dagger_generated {
    pub mod __private {
        pub use dagger_sdk::__private::*;
    }
}

#[dagger_sdk::object(root)]
pub struct Stateful {
    #[dagger(state)]
    value: String,
}

fn require_expected_state<T>()
where
    T: dagger_sdk::__private::ModuleObjectBridge<PersistentState = (u64,)>,
{
}

fn main() {
    require_expected_state::<Stateful>();
}
