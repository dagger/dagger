use dagger_sdk::{
    ContainerBuildOptsBuilder, HostDirectoryOpts, QueryContainerOpts, QueryContainerOptsBuilder,
};

static PLATFORMS: [&str; 2] = ["linux/arm64", "linux/x86_64"];

#[tokio::test]
async fn test_issue_30_alt() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    let context = client.host().directory_opts(
        ".",
        HostDirectoryOpts {
            exclude: Some(vec!["target", "client/node_modules", "client/build"]),
            include: None,
        },
    );

    for platform in PLATFORMS {
        let ref_ = client
            .container_opts(QueryContainerOpts {
                id: None,
                platform: Some(platform.to_string().into()),
            })
            .from("alpine")
            .exit_code()
            .await?;

        println!("published image to: {:#?}", ref_);
    }

    Ok(())
}

#[tokio::test]
async fn test_issue_30() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    let context = client.host().directory_opts(
        ".",
        HostDirectoryOpts {
            exclude: Some(vec!["target", "client/node_modules", "client/build"]),
            include: None,
        },
    );

    for platform in PLATFORMS {
        let ref_ = client
            .container_opts(
                QueryContainerOptsBuilder::default()
                    .platform(platform)
                    .build()
                    .unwrap(),
            )
            .from("alpine")
            .exit_code()
            .await?;

        println!("published image to: {:#?}", ref_);
    }

    Ok(())
}
