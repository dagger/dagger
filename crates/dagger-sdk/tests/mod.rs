use dagger_sdk::client::connect;
use dagger_sdk::gen::ContainerExecOpts;

#[test]
fn test_example_container() {
    let client = connect().unwrap();

    let alpine = client.container(None).from("alpine:3.16.2".into());

    let out = alpine
        .exec(Some(ContainerExecOpts {
            args: Some(vec!["cat".into(), "/etc/alpine-release".into()]),
            stdin: None,
            redirect_stdout: None,
            redirect_stderr: None,
            experimental_privileged_nesting: None,
        }))
        .stdout();

    assert_eq!(out, "3.16.2\n".to_string())
}
