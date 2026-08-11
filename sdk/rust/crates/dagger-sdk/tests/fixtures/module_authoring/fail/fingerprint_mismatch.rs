mod dagger_generated {
    pub mod __private {
        pub use dagger_sdk::__private::*;
    }
}

#[dagger_sdk::object(root)]
pub struct Fingerprinted {
    #[dagger(field)]
    value: String,
}

fn require_expected_fingerprint<T>()
where
    T: dagger_sdk::__private::ModuleObjectBridge<
            Fingerprint = dagger_sdk::__private::AuthoringFingerprint<0>,
        >,
{
}

fn main() {
    require_expected_fingerprint::<Fingerprinted>();
}
