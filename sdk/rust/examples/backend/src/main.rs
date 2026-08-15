mod configuration;

use clap::Parser;
use dagger_sdk::{Container, File, Query};
use eyre::Result;

use configuration::{Configuration, Output};

#[tokio::main]
async fn main() -> Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let configuration = Configuration::parse();
    let port = configuration.port;
    let output = configuration.into_output()?;
    let build = build_backend(&client);
    let image = build_prod_image(&client, build, port);
    match output {
        Output::BuildOnly => {
            let evaluated = image.sync().await?;
            println!("Service image built locally: {}", evaluated.id().await?);
        }
        Output::Publish(address) => {
            let image_reference = image.publish(address).await?;
            println!("Service image published at: {image_reference}");
        }
    }
    owned.close().await?;

    Ok(())
}

fn build_backend(client: &Query) -> File {
    let backend_directory = client.host().directory("axum-backend");
    let builder_image = client.container().from("rust:1.97.1-alpine3.22");
    builder_image
        .with_exec(vec!["apk", "add", "build-base", "musl"])
        .with_directory("./backend", backend_directory)
        .with_workdir("/backend")
        .with_exec(vec!["cargo", "build", "--release", "--locked"])
        .file("./target/release/axum-backend")
}

fn build_prod_image(client: &Query, build: File, port: u16) -> Container {
    client
        .container()
        .from("gcr.io/distroless/static-debian12")
        .with_file("/app/axum-backend", build)
        .with_env_variable("PORT", port.to_string())
        .with_exposed_port(i64::from(port))
        .with_entrypoint(vec!["/app/axum-backend"])
}
