use dagger_sdk as renamed_sdk;

mod dagger_generated {
    pub mod __private {
        pub use crate::renamed_sdk::__private::*;
    }
}

#[renamed_sdk::object(root, rename = "greeter")]
pub(crate) struct Greeter {
    #[dagger(field)]
    greeting: String,
    transient_counter: usize,
}

#[derive(Debug)]
struct GreetingError;

impl From<GreetingError> for renamed_sdk::ModuleError {
    fn from(_: GreetingError) -> Self {
        renamed_sdk::ModuleError::new("the greeting could not be produced")
    }
}

#[renamed_sdk::methods]
impl Greeter {
    #[dagger(constructor)]
    fn new(greeting: String) -> Self {
        Self {
            greeting,
            transient_counter: 0,
        }
    }

    #[dagger(function)]
    fn greet(
        &self,
        #[dagger(default = "world")] name: String,
    ) -> Result<String, GreetingError> {
        let _ = self.transient_counter;
        Ok(format!("{}, {name}", self.greeting))
    }
}

#[renamed_sdk::interface(rename = "named")]
pub(crate) trait Named {
    fn name(&self) -> String;
}

#[renamed_sdk::enum_type(rename = "mood")]
pub(crate) enum Mood {
    Happy,
    Sad,
}

#[renamed_sdk::scalar(rename = "slug")]
pub(crate) struct Slug(String);

fn main() {}
