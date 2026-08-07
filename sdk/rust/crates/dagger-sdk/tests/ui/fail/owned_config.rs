fn main() {
    let config = dagger_sdk::ClientConfig::builder().build().unwrap();
    let first = dagger_sdk::connect_with(config);
    let second = dagger_sdk::connect_with(config);
    let _ = (first, second);
}
