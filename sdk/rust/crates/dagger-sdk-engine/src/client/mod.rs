//! Standalone-client project planning boundaries.
//!
//! Client project code works only with validated operation-relative paths and semantic
//! identities. It neither receives module references nor gains filesystem, Cargo,
//! process, network, session, or engine authority through this module.

pub mod project;
