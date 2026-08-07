fn main() {
    let _ = std::mem::size_of::<dagger_sdk::core::config::Config>();
    let _ = std::mem::size_of::<dagger_sdk::query::Selection>();
    let _ = std::mem::size_of::<dagger_sdk::connector::DefaultConnector>();
    let _ = std::mem::size_of::<dagger_sdk::provision::DefaultCliProvisioner<(), ()>>();
    let _ = std::mem::size_of::<dagger_sdk::transport::ReqwestLoopbackConnection>();
    let _ = std::mem::size_of::<dagger_sdk::propagation::W3cPropagation>();
    let _ = std::mem::size_of::<dagger_sdk::session_startup::SessionResources>();
    let _ = std::mem::size_of::<dagger_sdk::compatibility::CompatibilityValidator>();
}
