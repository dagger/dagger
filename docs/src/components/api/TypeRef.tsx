import React from "react";
import Link from "@docusaurus/Link";
import type { TypeRef } from "./data";
import { useTypeHref } from "./data";
import TypeInfo from "./TypeInfo";
import styles from "./styles.module.scss";

// TypeRefView renders a structured type token tree as `[Directory!]!`, with
// every published core type cross-linked to its reference page. Named types
// that aren't core (String, Int, Module, ...) render as plain, colored tokens.
export default function TypeRefView({ type }: { type: TypeRef }): JSX.Element {
  const typeHref = useTypeHref();

  switch (type.kind) {
    case "nonNull":
      return (
        <>
          <TypeRefView type={type.of} />
          <span className={styles.punct}>!</span>
        </>
      );
    case "list":
      return (
        <>
          <span className={styles.punct}>[</span>
          <TypeRefView type={type.of} />
          <span className={styles.punct}>]</span>
        </>
      );
    case "named":
      if (type.isCore) {
        return (
          <Link className={styles.typeLink} to={typeHref(type.name)}>
            {type.name}
          </Link>
        );
      }
      // Non-core names carry a kind hint plus inline docs when the schema has
      // details worth revealing (enum options, input fields, custom scalars).
      return (
        <span className={styles.typeToken}>
          <span
            className={styles.typeName}
            data-kind={type.named}
            title={
              type.named === "object" ? type.name : `${type.name} (${type.named})`
            }
          >
            {type.name}
          </span>
          <TypeInfo name={type.name} kind={type.named} />
        </span>
      );
  }
}
