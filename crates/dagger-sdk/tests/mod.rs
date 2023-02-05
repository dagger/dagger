use dagger_sdk::client::connect;

#[test]
fn test_example_container() {
    let client = connect().unwrap();

    let alpine = client.container(None, None).from("alpine:3.16.2".into());

    let out = alpine
        .exec(
            Some(vec!["cat".into(), "/etc/alpine-release".into()]),
            None,
            None,
            None,
            None,
        )
        .stdout();

    assert_eq!(out, Some("3.16.2".to_string()))
}
