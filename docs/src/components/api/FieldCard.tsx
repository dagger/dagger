import React from "react";
import type { ApiField } from "./data";
import Signature from "./Signature";
import TypeRefView from "./TypeRef";
import Badge from "./Badge";
import Markdown, { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

// FieldCard is one anchored entry: a syntax-highlighted signature heading, any
// directive badges, the field's description, and a per-argument breakdown.
// The heading carries the field name as its id so it gets a stable anchor and
// a hover copy-link (the .hash-link affordance Docusaurus styles site-wide).
export default function FieldCard({
  field,
}: {
  field: ApiField;
}): JSX.Element {
  return (
    <div className={styles.card}>
      <h3 id={field.name} className={styles.cardHeading}>
        <Signature field={field} />
        <a
          className="hash-link"
          href={`#${field.name}`}
          aria-label={`Direct link to ${field.name}`}
          title={`Direct link to ${field.name}`}
        />
      </h3>

      <div className={styles.cardBody}>
        {(field.experimental || field.deprecated) && (
          <div className={styles.badges}>
            {field.experimental && (
              <Badge variant="experimental">Experimental</Badge>
            )}
            {field.deprecated && <Badge variant="deprecated">Deprecated</Badge>}
          </div>
        )}

        <Markdown className={styles.cardDesc}>{field.description}</Markdown>

        {field.deprecated?.reason && (
          <p className={styles.deprecatedNote}>
            <strong>Deprecated:</strong>{" "}
            <MarkdownInline>{field.deprecated.reason}</MarkdownInline>
          </p>
        )}
        {field.experimental?.reason && (
          <p className={styles.experimentalNote}>
            <strong>Experimental:</strong>{" "}
            <MarkdownInline>{field.experimental.reason}</MarkdownInline>
          </p>
        )}

        {field.args.length > 0 && (
          <table className={styles.args}>
            <thead>
              <tr>
                <th>Argument</th>
                <th>Type</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {field.args.map((arg) => (
                <tr key={arg.name}>
                  <td>
                    <code className={styles.argName}>{arg.name}</code>
                  </td>
                  <td>
                    <code>
                      <TypeRefView type={arg.type} />
                      {arg.defaultValue !== undefined && (
                        <span className={styles.literal}>
                          {" "}
                          = {arg.defaultValue}
                        </span>
                      )}
                    </code>
                  </td>
                  <td>
                    <MarkdownInline>{arg.description}</MarkdownInline>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
