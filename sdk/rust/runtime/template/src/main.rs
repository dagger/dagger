use dagger_sdk::*;

#[derive(Default)]
pub struct %s;

#[dagger_module]
impl %s {
    /// Returns a container that echoes whatever string argument is provided.
    #[dagger_function]
    fn container_echo(&self, string_arg: String) -> Container {
        dag().container().from("alpine:latest").with_exec(vec!["echo", &string_arg])
    }

    /// Returns lines that match a pattern in the files of the provided Directory.
    #[dagger_function]
    async fn grep_dir(
        &self,
        directory_arg: Directory,
        pattern: String,
    ) -> eyre::Result<String> {
        dag()
            .container()
            .from("alpine:latest")
            .with_mounted_directory("/mnt", directory_arg)
            .with_workdir("/mnt")
            .with_exec(vec!["grep", "-R", &pattern, "."])
            .stdout()
            .await
            .map_err(|e| eyre::eyre!(e))
    }
}

#[tokio::main]
async fn main() -> eyre::Result<()> {
    dagger_sdk::run(%s).await
}
