#[cfg(test)]
mod test {
    use rust::dagger;

    #[test]
    fn example_container() {
        let client = dagger::connect().unwrap();
        let alpine = client.container().from("alpine:3.16.2").unwrap();
        let out = alpine
            .exec(dagger::ContainerExecOpts {
                args: vec!["cat", "/etc/alpine-release"],
            })
            .stdout()
            .unwrap();

        assert_eq!("3.16.2", out);
    }
}
