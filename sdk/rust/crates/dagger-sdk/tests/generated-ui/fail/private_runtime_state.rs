fn private_runtime_state() {
    let _ = core::mem::size_of::<dagger_sdk::query::Selection>();
    let _ = core::mem::size_of::<dagger_sdk::lifecycle::SessionHandle>();
}

fn main() {}
