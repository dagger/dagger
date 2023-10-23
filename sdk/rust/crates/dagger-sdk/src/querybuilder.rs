use std::{
    collections::HashMap,
    ops::{Add, Deref},
    pin::Pin,
    sync::Arc,
};

use crate::core::graphql_client::DynGraphQLClient;
use futures::{future, Future};
use serde::{Deserialize, Serialize};

use crate::errors::{DaggerError, DaggerUnpackError};

pub fn query() -> Selection {
    Selection::default()
}

#[derive(Clone)]
struct LazyResolve(Arc<dyn Fn() -> Pin<Box<dyn Future<Output = String> + Send>> + Send + Sync>);

impl LazyResolve {
    pub fn new(
        func: Box<dyn Fn() -> Pin<Box<dyn Future<Output = String> + Send>> + Send + Sync>,
    ) -> Self {
        Self(Arc::new(func))
    }

    pub fn from_string(val: impl Into<String>) -> Self {
        let val: String = val.into();
        Self(Arc::new(move || Box::pin(future::ready(val.clone()))))
    }
}

impl Deref for LazyResolve {
    type Target = Arc<dyn Fn() -> Pin<Box<dyn Future<Output = String> + Send>> + Send + Sync>;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

impl From<String> for LazyResolve {
    fn from(value: String) -> Self {
        LazyResolve::from_string(value)
    }
}

#[derive(Clone, Default)]
pub struct Selection {
    name: Option<String>,
    alias: Option<String>,
    args: Option<HashMap<String, LazyResolve>>,

    prev: Option<Arc<Selection>>,
}

impl Selection {
    pub fn select_with_alias(&self, alias: &str, name: &str) -> Selection {
        Self {
            name: Some(name.to_string()),
            alias: Some(alias.to_string()),
            args: None,
            prev: Some(Arc::new(self.clone())),
        }
    }

    pub fn select(&self, name: &str) -> Selection {
        Self {
            name: Some(name.to_string()),
            alias: None,
            args: None,
            prev: Some(Arc::new(self.clone())),
        }
    }

    pub fn arg<S>(&self, name: &str, value: S) -> Selection
    where
        S: Serialize,
    {
        let mut s = self.clone();

        let val = serde_json::to_string(&value).unwrap();

        match s.args.as_mut() {
            Some(args) => {
                let _ = args.insert(name.to_string(), val.into());
            }
            None => {
                let mut hm = HashMap::new();
                let _ = hm.insert(name.to_string(), val.into());
                s.args = Some(hm);
            }
        }

        s
    }

    pub fn arg_lazy(
        &self,
        name: &str,
        value: Box<dyn Fn() -> Pin<Box<dyn Future<Output = String> + Send>> + Send + Sync>,
    ) -> Selection {
        let mut s = self.clone();

        match s.args.as_mut() {
            Some(args) => {
                let _ = args.insert(name.to_string(), LazyResolve::new(Box::new(value)));
            }
            None => {
                let mut hm = HashMap::new();
                let _ = hm.insert(name.to_string(), LazyResolve::new(Box::new(value)));
                s.args = Some(hm);
            }
        }

        s
    }

    pub fn arg_enum<S>(&self, name: &str, value: S) -> Selection
    where
        S: Serialize,
    {
        let mut s = self.clone();

        let val = serde_json::to_string(&value).unwrap();
        let val = val[1..val.len() - 1].to_string();

        match s.args.as_mut() {
            Some(args) => {
                let _ = args.insert(name.to_string(), val.into());
            }
            None => {
                let mut hm = HashMap::new();
                let _ = hm.insert(name.to_string(), val.into());
                s.args = Some(hm);
            }
        }

        s
    }

    pub async fn build(&self) -> Result<String, DaggerError> {
        let mut fields = vec!["query".to_string()];

        for sel in self.path() {
            if let Some(mut query) = sel.name.map(|q| q.clone()) {
                if let Some(args) = sel.args {
                    let mut actualargs = Vec::new();
                    for (name, arg) in args.iter() {
                        let arg = arg().await;
                        actualargs.push(format!("{name}:{arg}"));
                    }

                    query = query.add(&format!("({})", actualargs.join(", ")));
                }

                if let Some(alias) = sel.alias {
                    query = format!("{}:{}", alias, query);
                }

                fields.push(query);
            }
        }

        Ok(fields.join("{") + &"}".repeat(fields.len() - 1))
    }

    pub async fn execute<D>(&self, gql_client: DynGraphQLClient) -> Result<D, DaggerError>
    where
        D: for<'de> Deserialize<'de>,
    {
        let query = self.build().await?;

        tracing::trace!(query = query.as_str(), "dagger-query");

        let resp: Option<serde_json::Value> = match gql_client.query(&query).await {
            Ok(r) => r,
            Err(e) => return Err(DaggerError::Query(e)),
        };

        let resp: Option<D> = self.unpack_resp(resp)?;

        Ok(resp.unwrap())
    }

    fn path(&self) -> Vec<Selection> {
        let mut selections: Vec<Selection> = vec![];
        let mut cur = self;

        while cur.prev.is_some() {
            selections.push(cur.clone());

            if let Some(prev) = cur.prev.as_ref() {
                cur = prev;
            }
        }

        selections.reverse();
        selections
    }

    pub(crate) fn unpack_resp<D>(
        &self,
        resp: Option<serde_json::Value>,
    ) -> Result<Option<D>, DaggerError>
    where
        D: for<'de> Deserialize<'de>,
    {
        match resp {
            Some(r) => self.unpack_resp_value::<D>(r).map(|v| Some(v)),
            None => Ok(None),
        }
    }

    fn unpack_resp_value<D>(&self, r: serde_json::Value) -> Result<D, DaggerError>
    where
        D: for<'de> Deserialize<'de>,
    {
        if let Some(o) = r.as_object() {
            let keys = o.keys();
            if keys.len() != 1 {
                return Err(DaggerError::Unpack(DaggerUnpackError::TooManyNestedObjects));
            }

            let first = keys.into_iter().next().unwrap();
            return self.unpack_resp_value(o.get(first).unwrap().clone());
        }

        serde_json::from_value::<D>(r)
            .map_err(DaggerUnpackError::Deserialize)
            .map_err(DaggerError::Unpack)
    }
}

#[cfg(test)]
mod tests {
    use pretty_assertions::assert_eq;
    use serde::Serialize;

    use super::query;

    #[tokio::test]
    async fn test_query() {
        let root = query()
            .select("core")
            .select("image")
            .arg("ref", "alpine")
            .select("file")
            .arg("path", "/etc/alpine-release");

        let query = root.build().await.unwrap();

        assert_eq!(
            query,
            r#"query{core{image(ref:"alpine"){file(path:"/etc/alpine-release")}}}"#.to_string()
        )
    }

    #[tokio::test]
    async fn test_query_alias() {
        let root = query()
            .select("core")
            .select("image")
            .arg("ref", "alpine")
            .select_with_alias("foo", "file")
            .arg("path", "/etc/alpine-release");

        let query = root.build().await.unwrap();

        assert_eq!(
            query,
            r#"query{core{image(ref:"alpine"){foo:file(path:"/etc/alpine-release")}}}"#.to_string()
        )
    }

    #[tokio::test]
    async fn test_arg_collision() {
        let root = query()
            .select("a")
            .arg("arg", "one")
            .select("b")
            .arg("arg", "two");

        let query = root.build().await.unwrap();

        assert_eq!(query, r#"query{a(arg:"one"){b(arg:"two")}}"#.to_string())
    }

    #[tokio::test]
    async fn test_vec_arg() {
        let input = vec!["some-string"];

        let root = query().select("a").arg("arg", input);
        let query = root.build().await.unwrap();

        assert_eq!(query, r#"query{a(arg:["some-string"])}"#.to_string())
    }

    #[tokio::test]
    async fn test_ref_slice_arg() {
        let input = &["some-string"];

        let root = query().select("a").arg("arg", input);
        let query = root.build().await.unwrap();

        assert_eq!(query, r#"query{a(arg:["some-string"])}"#.to_string())
    }

    #[tokio::test]
    async fn test_stringb_arg() {
        let input = "some-string".to_string();

        let root = query().select("a").arg("arg", input);
        let query = root.build().await.unwrap();

        assert_eq!(query, r#"query{a(arg:"some-string")}"#.to_string())
    }

    #[tokio::test]
    async fn test_field_immutability() {
        let root = query().select("test");

        let a = root.select("a").build().await.unwrap();
        assert_eq!(a, r#"query{test{a}}"#.to_string());

        let b = root.select("b").build().await.unwrap();
        assert_eq!(b, r#"query{test{b}}"#.to_string());
    }

    #[derive(Serialize)]
    struct CustomType {
        pub name: String,
        pub s: Option<Box<CustomType>>,
    }

    #[tokio::test]
    async fn test_arg_custom_type() {
        let input = CustomType {
            name: "some-name".to_string(),
            s: Some(Box::new(CustomType {
                name: "some-other-name".to_string(),
                s: None,
            })),
        };

        let root = query().select("a").arg("arg", input);
        let query = root.build().await.unwrap();

        assert_eq!(
            query,
            r#"query{a(arg:{"name":"some-name","s":{"name":"some-other-name","s":null}})}"#
                .to_string()
        )
    }
}
