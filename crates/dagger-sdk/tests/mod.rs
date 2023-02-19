use dagger_sdk::{connect, ContainerExecOptsBuilder};

#[test]
fn test_example_container() {
    let client = connect().unwrap();

    let alpine = client.container(None).from("alpine:3.16.2");

    let out = alpine
        .exec(Some(
            ContainerExecOptsBuilder::default()
                .args(vec!["cat", "/etc/alpine-release"])
                .build()
                .unwrap(),
        ))
        .stdout()
        .unwrap();

    assert_eq!(out, "3.16.2\n".to_string())
}
