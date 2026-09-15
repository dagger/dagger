mod configuration;

use eyre::Result;
use dagger_sdk::{Query, File, Container};
use clap::Parser;

use configuration::Configuration;

#[tokio::main]
async fn main() -> Result<()> {
    let client = dagger_sdk::connect().await?;
    let build = build_backend(&client).await;
    let image = build_prod_image(&client, build).await;
    let image_reference = push_image(image).await?;
    println!("Image published at: {}", image_reference);
    Ok(())
}

async fn build_backend(client: &Query) -> File {
    let backend_directory = client.host().directory("axum-backend");
    client
        .container()
        .from("rust:1.77.2-alpine3.19")
        .with_exec(vec!["apk", "add", "build-base", "musl"])
        .with_directory("./backend", backend_directory)
        .with_workdir("/backend")
        .with_exec(vec!["cargo", "build", "--release"])
        .file("./target/release/axum-backend")
}

async fn build_prod_image(client: &Query, build: File) -> Container {
    let Configuration { port } = Configuration::parse();
    client
        .container()
        .from("gcr.io/distroless/static-debian12")
        .with_file(".", build)
        .with_env_variable("PORT", port.to_string())
        .with_entrypoint(vec!["./axum-backend"])
}

async fn push_image(image: Container) -> Result<String> {
    let tag_uuid = uuid::Uuid::new_v4().to_string();
    let address = format!("ttl.sh/backend-{}", tag_uuid);
    let image_reference = image.publish(address).await?;
    Ok(image_reference)
}
