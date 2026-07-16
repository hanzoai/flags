//! hanzo-flags-client — Rust client for the Hanzo native flags engine.
//!
//! Evaluates against cloud's `/v1/flags` (the stateless evaluator embedded in
//! hanzoai/cloud; PostHog-compatible shapes). For IN-PROCESS evaluation inside
//! hanzoai/cloud itself, use the `hanzo-flags` evaluator crate (cloud
//! `native/flags`) — this crate is the thin HTTP client for everyone else.
//!
//! ```no_run
//! # async fn demo() -> Result<(), Box<dyn std::error::Error>> {
//! use hanzo_flags_client::{Flags, Context};
//!
//! let flags = Flags::new("https://api.hanzo.ai").with_token(std::env::var("HANZO_API_KEY")?);
//! let res = flags.evaluate(Context::new("user-42").prop("plan", "pro")).await?;
//!
//! if res.is_enabled("new-nav") { /* ... */ }
//! if res.variant("checkout-exp") == Some("treatment") { /* ... */ }
//! # Ok(()) }
//! ```

use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;

pub struct Flags {
    host: String,
    token: Option<String>,
    project: Option<String>,
    http: reqwest::Client,
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct Context {
    pub distinct_id: String,
    #[serde(skip_serializing_if = "HashMap::is_empty")]
    pub person_properties: HashMap<String, Value>,
    #[serde(skip_serializing_if = "HashMap::is_empty")]
    pub groups: HashMap<String, GroupCtx>,
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct GroupCtx {
    pub key: String,
    #[serde(skip_serializing_if = "HashMap::is_empty")]
    pub properties: HashMap<String, Value>,
}

impl Context {
    pub fn new(distinct_id: impl Into<String>) -> Self {
        Self { distinct_id: distinct_id.into(), ..Default::default() }
    }
    pub fn prop(mut self, key: impl Into<String>, value: impl Into<Value>) -> Self {
        self.person_properties.insert(key.into(), value.into());
        self
    }
    pub fn group(mut self, type_index: i32, key: impl Into<String>) -> Self {
        self.groups.insert(type_index.to_string(), GroupCtx { key: key.into(), ..Default::default() });
        self
    }
}

/// PostHog-shaped evaluation response.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct EvalResult {
    #[serde(rename = "featureFlags", default)]
    pub feature_flags: HashMap<String, Value>,
    #[serde(rename = "featureFlagPayloads", default)]
    pub feature_flag_payloads: HashMap<String, Value>,
    #[serde(rename = "errorsWhileComputingFlags", default)]
    pub errors_while_computing_flags: bool,
}

impl EvalResult {
    pub fn is_enabled(&self, key: &str) -> bool {
        match self.feature_flags.get(key) {
            Some(Value::Bool(b)) => *b,
            Some(Value::String(s)) => !s.is_empty(),
            _ => false,
        }
    }
    pub fn variant(&self, key: &str) -> Option<&str> {
        match self.feature_flags.get(key) {
            Some(Value::String(s)) => Some(s.as_str()),
            _ => None,
        }
    }
    pub fn payload(&self, key: &str) -> Option<&Value> {
        self.feature_flag_payloads.get(key)
    }
}

impl Flags {
    pub fn new(host: impl Into<String>) -> Self {
        Self {
            host: host.into().trim_end_matches('/').to_string(),
            token: None,
            project: None,
            http: reqwest::Client::new(),
        }
    }
    pub fn with_token(mut self, token: impl Into<String>) -> Self {
        self.token = Some(token.into());
        self
    }
    pub fn with_project(mut self, project: impl Into<String>) -> Self {
        self.project = Some(project.into());
        self
    }

    pub async fn evaluate(&self, ctx: Context) -> Result<EvalResult, reqwest::Error> {
        let mut req = self.http.post(format!("{}/v1/flags", self.host)).json(&ctx);
        if let Some(t) = &self.token {
            req = req.bearer_auth(t);
        }
        if let Some(p) = &self.project {
            req = req.header("X-Project-Id", p);
        }
        req.send().await?.error_for_status()?.json().await
    }
}
