mod configuration;

use clap::Parser;
use dagger_sdk::{
    Container, ContainerExportOpts, File, ImageLayerCompression, ImageMediaTypes, Query,
};
use eyre::{Result, WrapErr as _};
use sha2::{Digest as _, Sha256};
use std::io::Read;

use configuration::{Configuration, Output};

#[tokio::main]
async fn main() -> Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let configuration = Configuration::parse();
    let port = configuration.port;
    let output = configuration.into_output()?;
    let (build, builder_image) = build_backend(&client);
    let (image, runtime_image) = build_prod_image(&client, build, port);
    match output {
        Output::BuildOnly {
            signoff_export: false,
        } => {
            let evaluated = image.sync().await?;
            println!("Service image built locally: {}", evaluated.id().await?);
        }
        Output::BuildOnly {
            signoff_export: true,
        } => {
            print_signoff_image_identity("rust:1.97.1-alpine3.22", &builder_image).await?;
            print_signoff_image_identity("gcr.io/distroless/static-debian12", &runtime_image)
                .await?;
            export_signoff_image(image, "standalone-example/backend").await?;
        }
        Output::Publish(address) => {
            let image_reference = image.publish(address).await?;
            println!("Service image published at: {image_reference}");
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

const SIGNOFF_OUTPUT: &str = "build/backend-image.tar";
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
        .wrap_err("export the backend image as an OCI/Gzip archive")?;
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

fn build_backend(client: &Query) -> (File, Container) {
    let backend_directory = client.host().directory("axum-backend");
    let builder_image = client.container().from("rust:1.97.1-alpine3.22");
    let build = builder_image
        .clone()
        .with_exec(vec!["apk", "add", "build-base", "musl"])
        .with_directory("./backend", backend_directory)
        .with_workdir("/backend")
        .with_exec(vec!["cargo", "build", "--release", "--locked"])
        .file("./target/release/axum-backend");
    (build, builder_image)
}

fn build_prod_image(client: &Query, build: File, port: u16) -> (Container, Container) {
    let runtime_image = client.container().from("gcr.io/distroless/static-debian12");
    let image = runtime_image
        .clone()
        .with_file("/app/axum-backend", build)
        .with_env_variable("PORT", port.to_string())
        .with_exposed_port(i64::from(port))
        .with_entrypoint(vec!["/app/axum-backend"]);
    (image, runtime_image)
}
