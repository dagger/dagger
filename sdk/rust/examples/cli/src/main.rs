use std::io::Read as _;

use dagger_sdk::Container;
use eyre::WrapErr as _;
use sha2::{Digest as _, Sha256};

const BUILDER_IMAGE: &str = "rust:1.97.1-slim-bookworm";
const OUTPUT: &str = "build/cli";
const OUTPUT_MAX_BYTES: u64 = 256 * 1024 * 1024;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let app_directory = client.host().directory("./app");

    let builder_image = client.container().from(BUILDER_IMAGE);
    let build_file = builder_image
        .clone()
        .with_directory("./app", app_directory)
        .with_workdir("/app")
        .with_exec(vec!["cargo", "build", "--release", "--locked"])
        .file("./target/release/app");

    let exported = build_file.export(format!("./{OUTPUT}")).await?;
    if exported.is_empty() {
        eyre::bail!("Dagger did not export the built CLI");
    }
    if std::env::var("RUST_SDK_SIGNOFF_PROGRAM").as_deref() == Ok("standalone-example/cli") {
        print_signoff_image_identity(BUILDER_IMAGE, &builder_image).await?;
        let (size, digest) = bounded_file_identity(OUTPUT)?;
        println!("Sign-off output: {OUTPUT} sha256:{digest} {size} bytes");
    }
    println!("CLI built at {exported}");
    owned.close().await?;

    Ok(())
}

async fn print_signoff_image_identity(reference: &str, image: &Container) -> eyre::Result<()> {
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

fn bounded_file_identity(path: &str) -> eyre::Result<(u64, String)> {
    let mut file = std::fs::File::open(path)
        .wrap_err_with(|| format!("open the sign-off output at {path}"))?;
    let mut hasher = Sha256::new();
    let mut size = 0_u64;
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        let read = file
            .read(&mut buffer)
            .wrap_err_with(|| format!("read the sign-off output at {path}"))?;
        if read == 0 {
            break;
        }
        size = size
            .checked_add(u64::try_from(read)?)
            .ok_or_else(|| eyre::eyre!("sign-off output size overflowed"))?;
        if size > OUTPUT_MAX_BYTES {
            eyre::bail!("sign-off output exceeds the {OUTPUT_MAX_BYTES}-byte bound");
        }
        hasher.update(&buffer[..read]);
    }
    Ok((size, format!("{:x}", hasher.finalize())))
}
