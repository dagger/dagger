use dagger_sdk::{connect, ContainerExecOptsBuilder};

#[tokio::test]
async fn test_example_container() {
    let client = connect().await.unwrap();

    let alpine = client.container().from("alpine:3.16.2");

    let out = alpine
        .exec_opts(
            ContainerExecOptsBuilder::default()
                .args(vec!["cat", "/etc/alpine-release"])
                .build()
                .unwrap(),
        )
        .stdout()
        .await
        .unwrap();

    assert_eq!(out, "3.16.2\n".to_string())
}
