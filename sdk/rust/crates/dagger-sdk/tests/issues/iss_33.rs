use dagger_sdk::{ContainerWithExposedPortOpts, NetworkProtocol};

#[tokio::test]
async fn test_issue_30_alt() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    client
        .container()
        .from("denoland/deno:debian-1.30.3")
        .with_exposed_port_opts(
            53,
            ContainerWithExposedPortOpts {
                protocol: Some(NetworkProtocol::TCP),
                description: None,
            },
        )
        .with_exposed_port_opts(
            53,
            ContainerWithExposedPortOpts {
                protocol: Some(NetworkProtocol::UDP),
                description: None,
            },
        )
        .with_exec(vec!["echo", "hello"])
        .sync()
        .await?;

    Ok(())
}
