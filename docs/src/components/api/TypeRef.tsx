import React from "react";
import Link from "@docusaurus/Link";
import type { TypeRef } from "./data";
import { typeHref } from "./data";
import styles from "./styles.module.scss";

// TypeRefView renders a structured type token tree as `[Directory!]!`, with
// every published core type cross-linked to its reference page. Named types
// that aren't core (String, Int, Module, ...) render as plain, colored tokens.
export default function TypeRefView({ type }: { type: TypeRef }): JSX.Element {
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
      return <span className={styles.typeName}>{type.name}</span>;
  }
}
