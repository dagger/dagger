use clap::{Parser, Subcommand};
use dagger_sdk::{Container, Directory, Query};
use eyre::Result;

#[derive(Parser)]
#[command(version, about)]
struct Configuration {
    #[command(subcommand)]
    action: Option<Action>,
}

#[derive(Subcommand)]
enum Action {
    /// Build and evaluate the web image without publishing it.
    Build,
    /// Publish the web image to an explicitly selected address.
    Publish {
        /// Complete registry address to publish.
        #[arg(long)]
        address: String,
        /// Confirms the external registry write.
        #[arg(long)]
        allow_publish: bool,
    },
}

#[tokio::main]
async fn main() -> Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let configuration = Configuration::parse();
    let build_directory = build_frontend(&client);
    let image = build_prod_image(&client, build_directory);
    match configuration.action {
        None | Some(Action::Build) => {
            let evaluated = image.sync().await?;
            println!("Web image built locally: {}", evaluated.id().await?);
        }
        Some(Action::Publish {
            address,
            allow_publish: true,
        }) => {
            let image_reference = image.publish(address).await?;
            println!("Web image published at: {image_reference}");
        }
        Some(Action::Publish { .. }) => {
            eyre::bail!("publishing requires the explicit --allow-publish confirmation");
        }
    }
    owned.close().await?;

    Ok(())
}

fn build_frontend(client: &Query) -> Directory {
    let frontend_directory = client.host().directory("leptos-frontend");
    let builder_image = client.container().from("rust:1.97.1");
    builder_image
        .with_exec(vec!["apt-get", "update"])
        .with_exec(vec!["apt-get", "install", "-y", "nodejs", "npm"])
        .with_exec(vec!["rustup", "target", "add", "wasm32-unknown-unknown"])
        .with_exec(vec![
            "cargo",
            "install",
            "trunk",
            "--version",
            "0.21.14",
            "--locked",
        ])
        .with_directory("./frontend", frontend_directory)
        .with_workdir("/frontend")
        .with_exec(vec!["trunk", "build", "--release", "--locked"])
        .directory("./dist")
}

fn build_prod_image(client: &Query, build_directory: Directory) -> Container {
    client
        .container()
        .from("nginx:1.24.0-alpine3.17")
        .with_directory("/usr/share/nginx/html", build_directory)
}
