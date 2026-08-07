//! Instance-local W3C propagation for child processes and loopback requests.
//!
//! The component owns its propagator and captured process carrier. It never consults
//! or mutates OpenTelemetry's global propagator, so independently configured SDK
//! clients and concurrent requests cannot overwrite one another's propagation state.

use std::collections::HashMap;
use std::error::Error;
use std::ffi::OsString;
use std::fmt;

use opentelemetry::Context;
use opentelemetry::propagation::{
    Extractor, Injector, TextMapCompositePropagator, TextMapPropagator,
};
use opentelemetry::trace::TraceContextExt;
use opentelemetry_sdk::propagation::{BaggagePropagator, TraceContextPropagator};
use reqwest::header::{HeaderMap, HeaderName, HeaderValue};
use tracing_opentelemetry::OpenTelemetrySpanExt;

use crate::preflight::PropagationEnvironment;

/// Local W3C Trace Context and Baggage policy for one connection attempt.
pub(crate) struct W3cPropagation {
    propagator: TextMapCompositePropagator,
    inherited: StringCarrier,
}

impl W3cPropagation {
    pub(crate) fn new(inherited: PropagationEnvironment) -> Self {
        let inherited = inherited
            .values()
            .into_iter()
            .filter_map(|(key, value)| {
                value
                    .and_then(|value| value.to_str())
                    .map(|value| (key.to_ascii_lowercase(), value.to_owned()))
            })
            .collect();
        Self {
            // This is deliberately an owned composite rather than the process-global
            // propagator: callers may configure global telemetry independently.
            propagator: TextMapCompositePropagator::new(vec![
                Box::new(TraceContextPropagator::new()),
                Box::new(BaggagePropagator::new()),
            ]),
            inherited: StringCarrier(inherited),
        }
    }

    pub(crate) fn child_environment(&self) -> Vec<(OsString, OsString)> {
        let mut carrier = StringCarrier::default();
        let context = self.selected_context();
        self.propagator.inject_context(&context, &mut carrier);
        carrier
            .0
            .into_iter()
            .map(|(key, value)| {
                (
                    OsString::from(key.to_ascii_uppercase()),
                    OsString::from(value),
                )
            })
            .collect()
    }

    pub(crate) fn request_headers(&self) -> Result<HeaderMap, PropagationError> {
        let mut carrier = StringCarrier::default();
        let context = self.selected_context();
        self.propagator.inject_context(&context, &mut carrier);

        let mut headers = HeaderMap::with_capacity(carrier.0.len());
        for (key, value) in carrier.0 {
            let name = HeaderName::from_bytes(key.as_bytes())
                .map_err(|_| PropagationError::InvalidHeader)?;
            let value =
                HeaderValue::from_str(&value).map_err(|_| PropagationError::InvalidHeader)?;
            headers.insert(name, value);
        }
        Ok(headers)
    }

    fn selected_context(&self) -> Context {
        let tracing_context = tracing::Span::current().context();
        if context_has_valid_span(&tracing_context) {
            return tracing_context;
        }

        let attached = Context::current();
        if context_has_valid_span(&attached) {
            return attached;
        }

        // Extraction is repeated into a fresh immutable Context for every request.
        // This preserves valid baggage even when an inherited traceparent is absent or
        // malformed, while the official propagator omits invalid trace state.
        self.propagator.extract(&self.inherited)
    }

    #[cfg(test)]
    pub(crate) fn inherited_headers_for_test(&self) -> Result<HeaderMap, PropagationError> {
        let mut carrier = StringCarrier::default();
        let context = self.propagator.extract(&self.inherited);
        self.propagator.inject_context(&context, &mut carrier);
        let mut headers = HeaderMap::new();
        for (key, value) in carrier.0 {
            let name = HeaderName::from_bytes(key.as_bytes())
                .map_err(|_| PropagationError::InvalidHeader)?;
            let value =
                HeaderValue::from_str(&value).map_err(|_| PropagationError::InvalidHeader)?;
            headers.insert(name, value);
        }
        Ok(headers)
    }
}

fn context_has_valid_span(context: &Context) -> bool {
    context.span().span_context().is_valid()
}

#[derive(Default)]
struct StringCarrier(HashMap<String, String>);

impl Injector for StringCarrier {
    fn set(&mut self, key: &str, value: String) {
        self.0.insert(key.to_owned(), value);
    }
}

impl Extractor for StringCarrier {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).map(String::as_str)
    }

    fn keys(&self) -> Vec<&str> {
        self.0.keys().map(String::as_str).collect()
    }
}

/// A local propagation value could not be represented as an HTTP header.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum PropagationError {
    InvalidHeader,
}

impl fmt::Display for PropagationError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("the selected W3C propagation context is invalid")
    }
}

impl Error for PropagationError {}
