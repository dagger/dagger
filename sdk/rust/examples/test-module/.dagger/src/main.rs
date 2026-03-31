use dagger_sdk::*;

#[derive(Default)]
pub struct TestModule;

/// A test module to verify the Rust SDK integration in the dagger source tree.
#[dagger_module]
impl TestModule {
    /// Returns a greeting.
    #[dagger_function]
    fn hello(&self) -> String {
        "Hello from the dagger source tree!".to_string()
    }

    /// Echoes a message via an alpine container.
    #[dagger_function]
    async fn container_echo(&self, msg: String) -> eyre::Result<String> {
        dag()
            .container()
            .from("alpine:latest")
            .with_exec(vec!["echo", &msg])
            .stdout()
            .await
            .map_err(|e| eyre::eyre!(e))
    }
}

#[tokio::main]
async fn main() -> eyre::Result<()> {
    dagger_sdk::run(TestModule).await
}
