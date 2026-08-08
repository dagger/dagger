//! Rust projection for the transitional generated-client surface.
//!
//! This module preserves the current public generated API while the canonical schema
//! and projection stages are built out. It emits syntax tokens and never performs
//! formatting, filesystem access, or process execution.

mod client;

pub use client::RustGenerator;
pub(crate) use client::candidate_text;
