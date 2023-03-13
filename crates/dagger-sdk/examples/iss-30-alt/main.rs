#![feature(async_closure)]

use dagger_sdk::{ContainerBuildOptsBuilder, HostDirectoryOpts, QueryContainerOpts};

static DOCKER_FILES: [&str; 3] = ["Dockerfile", "Dockerfile.alpine", "Dockerfile.distroless"];
static PLATFORMS: [&str; 2] = ["linux/arm64", "linux/x86_64"];

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    let context = client.host().directory_opts(
        ".",
        HostDirectoryOpts {
            exclude: Some(vec!["target", "client/node_modules", "client/build"]),
            include: None,
        },
    );

    for file in DOCKER_FILES {
        for platform in PLATFORMS {
            let ref_ = client
                .container_opts(QueryContainerOpts {
                    id: None,
                    platform: Some(platform.to_string().into()),
                })
                .build_opts(
                    context.id().await?,
                    ContainerBuildOptsBuilder::default()
                        .dockerfile(file)
                        .build()
                        .unwrap(),
                )
                .export("./test")
                .await?;

            println!("published image to: {:#?}", ref_);
        }
    }

    Ok(())
}
