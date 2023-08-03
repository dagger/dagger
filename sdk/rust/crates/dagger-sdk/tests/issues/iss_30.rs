use dagger_sdk::{QueryContainerOpts, QueryContainerOptsBuilder};

static PLATFORMS: [&str; 2] = ["linux/arm64", "linux/x86_64"];

#[tokio::test]
async fn test_issue_30_alt() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    for platform in PLATFORMS {
        let ref_ = client
            .container_opts(QueryContainerOpts {
                id: None,
                platform: Some(platform.to_string().into()),
            })
            .from("alpine")
            .with_exec(vec!["echo", "'hello'"])
            .sync()
            .await?;

        println!("published image to: {:#?}", ref_);
    }

    Ok(())
}

#[tokio::test]
async fn test_issue_30() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    for platform in PLATFORMS {
        let ref_ = client
            .container_opts(
                QueryContainerOptsBuilder::default()
                    .platform(platform)
                    .build()
                    .unwrap(),
            )
            .from("alpine")
            .with_exec(vec!["echo", "'hello'"])
            .sync()
            .await?;

        println!("published image to: {:#?}", ref_);
    }

    Ok(())
}
