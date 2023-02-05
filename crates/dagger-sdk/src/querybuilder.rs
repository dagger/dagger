use std::{collections::HashMap, ops::Add, sync::Arc};

use futures::executor::block_on;
use serde::{Deserialize, Serialize};

pub fn query() -> Selection {
    Selection::default()
}

impl Default for Selection {
    fn default() -> Self {
        Self {
            name: Default::default(),
            alias: Default::default(),
            args: Default::default(),
            prev: Default::default(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct Selection {
    name: Option<String>,
    alias: Option<String>,
    args: Option<HashMap<String, String>>,

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

    pub fn arg<S>(&self, name: &str, value: S) -> eyre::Result<Selection>
    where
        S: Serialize,
    {
        let mut s = self.clone();

        let val = serde_json::to_string(&value)?;

        match s.args.as_mut() {
            Some(args) => {
                let _ = args.insert(name.to_string(), val);
            }
            None => {
                let mut hm = HashMap::new();
                let _ = hm.insert(name.to_string(), val);
                s.args = Some(hm);
            }
        }

        Ok(s)
    }

    pub fn build(&self) -> eyre::Result<String> {
        let mut fields = vec!["query".to_string()];

        for sel in self.path() {
            if let Some(mut query) = sel.name.map(|q| q.clone()) {
                if let Some(args) = sel.args {
                    let actualargs = args
                        .iter()
                        .map(|(name, arg)| format!("{name}:{arg}"))
                        .collect::<Vec<_>>();

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

    pub fn execute<D>(&self, gql_client: &gql_client::Client) -> eyre::Result<Option<D>>
    where
        D: for<'de> Deserialize<'de>,
    {
        let query = self.build()?;

        let resp: Option<D> = match block_on(gql_client.query(&query)) {
            Ok(r) => r,
            Err(e) => eyre::bail!(e),
        };

        Ok(resp)
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
}

#[cfg(test)]
mod tests {
    use pretty_assertions::assert_eq;
    use serde::Serialize;

    use super::query;

    #[test]
    fn test_query() {
        let root = query()
            .select("core")
            .select("image")
            .arg("ref", "alpine")
            .unwrap()
            .select("file")
            .arg("path", "/etc/alpine-release")
            .unwrap();

        let query = root.build().unwrap();

        assert_eq!(
            query,
            r#"query{core{image(ref:"alpine"){file(path:"/etc/alpine-release")}}}"#.to_string()
        )
    }

    #[test]
    fn test_query_alias() {
        let root = query()
            .select("core")
            .select("image")
            .arg("ref", "alpine")
            .unwrap()
            .select_with_alias("foo", "file")
            .arg("path", "/etc/alpine-release")
            .unwrap();

        let query = root.build().unwrap();

        assert_eq!(
            query,
            r#"query{core{image(ref:"alpine"){foo:file(path:"/etc/alpine-release")}}}"#.to_string()
        )
    }

    #[test]
    fn test_arg_collision() {
        let root = query()
            .select("a")
            .arg("arg", "one")
            .unwrap()
            .select("b")
            .arg("arg", "two")
            .unwrap();

        let query = root.build().unwrap();

        assert_eq!(query, r#"query{a(arg:"one"){b(arg:"two")}}"#.to_string())
    }

    #[test]
    fn test_vec_arg() {
        let input = vec!["some-string"];

        let root = query().select("a").arg("arg", input).unwrap();
        let query = root.build().unwrap();

        assert_eq!(query, r#"query{a(arg:["some-string"])}"#.to_string())
    }

    #[test]
    fn test_ref_slice_arg() {
        let input = &["some-string"];

        let root = query().select("a").arg("arg", input).unwrap();
        let query = root.build().unwrap();

        assert_eq!(query, r#"query{a(arg:["some-string"])}"#.to_string())
    }

    #[test]
    fn test_stringb_arg() {
        let input = "some-string".to_string();

        let root = query().select("a").arg("arg", input).unwrap();
        let query = root.build().unwrap();

        assert_eq!(query, r#"query{a(arg:"some-string")}"#.to_string())
    }

    #[test]
    fn test_field_immutability() {
        let root = query().select("test");

        let a = root.select("a").build().unwrap();
        assert_eq!(a, r#"query{test{a}}"#.to_string());

        let b = root.select("b").build().unwrap();
        assert_eq!(b, r#"query{test{b}}"#.to_string());
    }

    #[derive(Serialize)]
    struct CustomType {
        pub name: String,
        pub s: Option<Box<CustomType>>,
    }

    #[test]
    fn test_arg_custom_type() {
        let input = CustomType {
            name: "some-name".to_string(),
            s: Some(Box::new(CustomType {
                name: "some-other-name".to_string(),
                s: None,
            })),
        };

        let root = query().select("a").arg("arg", input).unwrap();
        let query = root.build().unwrap();

        assert_eq!(
            query,
            r#"query{a(arg:{"name":"some-name","s":{"name":"some-other-name","s":null}})}"#
                .to_string()
        )
    }
}
