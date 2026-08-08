#![deny(warnings)]
//! Deterministic repository orchestration for Dagger Rust SDK code generation.
//!
//! Pure schema projection and rendering remain in `dagger-codegen`. This crate owns
//! the narrower process and filesystem boundary: checked input loading, pinned
//! formatting, drift verification, and explicit failure-atomic publication.

pub mod cli;
pub mod generate;
