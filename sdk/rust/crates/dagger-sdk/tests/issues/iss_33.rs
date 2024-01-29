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
                protocol: Some(NetworkProtocol::Tcp),
                description: None,
                experimental_skip_healthcheck: None,
            },
        )
        .with_exposed_port_opts(
            53,
            ContainerWithExposedPortOpts {
                protocol: Some(NetworkProtocol::Udp),
                description: None,
                experimental_skip_healthcheck: None,
            },
        )
        .with_exec(vec!["echo", "hello"])
        .sync()
        .await?;

    Ok(())
}
