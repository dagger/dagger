fn main() -> eyre::Result<()> {
    let client = dagger_sdk::client::connect()?;

    let version = client
        .container(None)
        .from("golang:1.19".into())
        .with_exec(vec!["go".into(), "version".into()], None)
        .stdout();

    println!("Hello from Dagger and {}", version.trim());

    Ok(())
}
