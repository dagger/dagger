use dagger_sdk::{Container, Directory, Query};
use eyre::Result;

#[tokio::main]
async fn main() -> Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let build_directory = build_frontend(&client).await;
    let image = build_prod_image(&client, build_directory).await;
    let image_reference = push_image(image).await?;
    println!("Image published at: {image_reference}");
    owned.close().await?;

    Ok(())
}

async fn build_frontend(client: &Query) -> Directory {
    let backend_directory = client.host().directory("leptos-frontend");
    client
        .container()
        .from("rust:1.97.1")
        .with_exec(vec!["apt-get", "update"])
        .with_exec(vec!["apt-get", "install", "-y", "nodejs", "npm"])
        .with_exec(vec!["rustup", "target", "add", "wasm32-unknown-unknown"])
        .with_exec(vec!["cargo", "install", "trunk"])
        .with_directory("./frontend", backend_directory)
        .with_workdir("/frontend")
        .with_exec(vec!["trunk", "build", "--release"])
        .directory("./dist")
}

async fn build_prod_image(client: &Query, build_directory: Directory) -> Container {
    client
        .container()
        .from("nginx:1.24.0-alpine3.17")
        .with_directory("/usr/share/nginx/html", build_directory)
}

async fn push_image(image: Container) -> Result<String> {
    let tag_uuid = uuid::Uuid::new_v4().to_string();
    let address = format!("ttl.sh/frontend-{}", tag_uuid);
    let image_reference = image.publish(address).await?;
    Ok(image_reference)
}
