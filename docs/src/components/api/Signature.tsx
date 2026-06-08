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
function Arg({
  arg,
  trailingComma,
}: {
  arg: ApiField["args"][number];
  trailingComma: boolean;
}): JSX.Element {
  return (
    <>
      <span className={styles.argName}>{arg.name}</span>
      <span className={styles.punct}>: </span>
      <TypeRefView type={arg.type} />
      {arg.defaultValue !== undefined && (
        <>
          <span className={styles.punct}> = </span>
          <span className={styles.literal}>{arg.defaultValue}</span>
        </>
      )}
      {trailingComma && <span className={styles.punct}>,</span>}
    </>
  );
}

export default function Signature({ field }: { field: ApiField }): JSX.Element {
  // With more than two arguments, break the signature across lines: the opening
  // paren stays on the name's line, each argument sits on its own indented
  // line, and the closing paren + return type get a line of their own. So a
  // wide signature reads as a proper block instead of `asService(   foo: 123)`.
  const multiline = field.args.length > 2;
  const fieldLink = (
    <a className={styles.fieldName} href={`#${field.name}`}>
      {field.name}
    </a>
  );

  if (multiline) {
    return (
      <code className={`${styles.signature} ${styles.signatureMultiline}`}>
        {fieldLink}
        <span className={styles.punct}>(</span>
        <span className={styles.argsBlock}>
          {field.args.map((arg, i) => (
            <span key={arg.name} className={styles.argLine}>
              <Arg arg={arg} trailingComma={i < field.args.length - 1} />
            </span>
          ))}
        </span>
        <span className={styles.retLine}>
          <span className={styles.punct}>): </span>
          <TypeRefView type={field.type} />
        </span>
      </code>
    );
  }

  return (
    <code className={styles.signature}>
      {fieldLink}
      {field.args.length > 0 && (
        <>
          <span className={styles.punct}>(</span>
          {field.args.map((arg, i) => (
            <React.Fragment key={arg.name}>
              <Arg arg={arg} trailingComma={false} />
              {i < field.args.length - 1 && (
                <span className={styles.punct}>, </span>
              )}
            </React.Fragment>
          ))}
          <span className={styles.punct}>)</span>
        </>
      )}
      <span className={styles.punct}>: </span>
      <TypeRefView type={field.type} />
    </code>
  );
}
