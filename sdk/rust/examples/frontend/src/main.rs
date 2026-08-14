use clap::{Parser, Subcommand};
use dagger_sdk::{
    Container, ContainerExportOpts, Directory, ImageLayerCompression, ImageMediaTypes, Query,
};
use eyre::{Result, WrapErr as _};
use sha2::{Digest as _, Sha256};
use std::io::Read;

#[derive(Parser)]
#[command(version, about)]
struct Configuration {
    #[command(subcommand)]
    action: Option<Action>,
}

#[derive(Subcommand)]
enum Action {
    /// Build and evaluate the web image without publishing it.
    Build {
        /// Exports the evaluated image for the bounded exact-target sign-off inspection.
        #[arg(long, hide = true)]
        signoff_export: bool,
    },
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
    let (build_directory, builder_image) = build_frontend(&client);
    let (image, runtime_image) = build_prod_image(&client, build_directory);
    match configuration.action {
        None
        | Some(Action::Build {
            signoff_export: false,
        }) => {
            let evaluated = image.sync().await?;
            println!("Web image built locally: {}", evaluated.id().await?);
        }
        Some(Action::Build {
            signoff_export: true,
        }) => {
            print_signoff_image_identity("rust:1.97.1", &builder_image).await?;
            print_signoff_image_identity("nginx:1.24.0-alpine3.17", &runtime_image).await?;
            export_signoff_image(image, "standalone-example/frontend").await?;
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

async fn print_signoff_image_identity(reference: &str, image: &Container) -> Result<()> {
    let resolved = image
        .sync()
        .await
        .wrap_err_with(|| format!("resolve the public image dependency {reference}"))?;
    let id = resolved
        .id()
        .await
        .wrap_err_with(|| format!("identify the resolved public image dependency {reference}"))?;
    println!(
        "Sign-off image resolved: {reference} sha256:{:x}",
        Sha256::digest(id.as_str().as_bytes())
    );
    Ok(())
}

const SIGNOFF_OUTPUT: &str = "build/frontend-image.tar";
// Match the Rust-owned fixture and scanner bound so an example cannot hand an unexpectedly large
// archive to the post-build evidence path.
const SIGNOFF_OUTPUT_MAX_BYTES: u64 = 256 * 1024 * 1024;

async fn export_signoff_image(image: Container, expected_program: &str) -> Result<()> {
    // The hidden switch is intentionally useless outside the isolated sign-off branch; this keeps
    // ordinary example use build-only without creating a second general-purpose export surface.
    if std::env::var("RUST_SDK_SIGNOFF_PROGRAM").as_deref() != Ok(expected_program) {
        eyre::bail!("the hidden image export is reserved for exact-target SDK sign-off");
    }
    std::fs::create_dir_all("build").wrap_err("create the bounded sign-off output directory")?;
    let options = ContainerExportOpts::default()
        .with_media_types(ImageMediaTypes::OciMediaTypes)
        .with_forced_compression(ImageLayerCompression::Gzip);
    image
        .export_opts(SIGNOFF_OUTPUT, &options)
        .await
        .wrap_err("export the frontend image as an OCI/Gzip archive")?;
    let (size, digest) = bounded_file_identity(SIGNOFF_OUTPUT)?;
    println!("Sign-off image exported: {SIGNOFF_OUTPUT} sha256:{digest} {size} bytes");
    Ok(())
}

fn bounded_file_identity(path: &str) -> Result<(u64, String)> {
    let mut file =
        std::fs::File::open(path).wrap_err_with(|| format!("open the sign-off image at {path}"))?;
    let mut hasher = Sha256::new();
    let mut size = 0_u64;
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        let read = file
            .read(&mut buffer)
            .wrap_err_with(|| format!("read the sign-off image at {path}"))?;
        if read == 0 {
            break;
        }
        size = size
            .checked_add(u64::try_from(read)?)
            .ok_or_else(|| eyre::eyre!("sign-off image size overflowed"))?;
        if size > SIGNOFF_OUTPUT_MAX_BYTES {
            eyre::bail!("sign-off image exceeds the {SIGNOFF_OUTPUT_MAX_BYTES}-byte bound");
        }
        hasher.update(&buffer[..read]);
    }
    Ok((size, format!("{:x}", hasher.finalize())))
}

fn build_frontend(client: &Query) -> (Directory, Container) {
    let frontend_directory = client.host().directory("leptos-frontend");
    let builder_image = client.container().from("rust:1.97.1");
    let build = builder_image
        .clone()
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
        .directory("./dist");
    (build, builder_image)
}

fn build_prod_image(client: &Query, build_directory: Directory) -> (Container, Container) {
    let runtime_image = client.container().from("nginx:1.24.0-alpine3.17");
    let image = runtime_image
        .clone()
        .with_directory("/usr/share/nginx/html", build_directory);
    (image, runtime_image)
}
