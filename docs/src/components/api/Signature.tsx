import React from "react";
import type { ApiField } from "./data";
import TypeRefView from "./TypeRef";
import styles from "./styles.module.scss";

// Signature renders a field's call signature in GraphQL-ish form, the way the
// Dang reference renders a builtin's signature:
//
//   withNewFile(path: String!, contents: String!, permissions: Int = 420): Directory!
//
// Long argument lists wrap one-per-line so wide signatures stay readable.
export default function Signature({ field }: { field: ApiField }): JSX.Element {
  const multiline = field.args.length > 2;
  return (
    <code className={styles.signature}>
      <span className={styles.fieldName}>{field.name}</span>
      {field.args.length > 0 && (
        <>
          <span className={styles.punct}>(</span>
          <span className={multiline ? styles.argsBlock : undefined}>
            {field.args.map((arg, i) => (
              <span
                key={arg.name}
                className={multiline ? styles.argLine : undefined}
              >
                <span className={styles.argName}>{arg.name}</span>
                <span className={styles.punct}>: </span>
                <TypeRefView type={arg.type} />
                {arg.defaultValue !== undefined && (
                  <>
                    <span className={styles.punct}> = </span>
                    <span className={styles.literal}>{arg.defaultValue}</span>
                  </>
                )}
                {i < field.args.length - 1 && (
                  <span className={styles.punct}>,{multiline ? "" : " "}</span>
                )}
              </span>
            ))}
          </span>
          <span className={styles.punct}>)</span>
        </>
      )}
      <span className={styles.punct}>: </span>
      <TypeRefView type={field.type} />
    </code>
  );
}
