import React from "react";
import useBrokenLinks from "@docusaurus/useBrokenLinks";
import { returnKind, type ApiField } from "./data";
import Signature from "./Signature";
import TypeRefView from "./TypeRef";
import Badge from "./Badge";
import Markdown, { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

// FieldCard is one anchored entry: a syntax-highlighted signature heading, any
// directive badges, the field's description, and a per-argument breakdown.
// The heading carries the field name as its id so it gets a stable anchor.
export default function FieldCard({
  field,
}: {
  field: ApiField;
}): JSX.Element {
  const brokenLinks = useBrokenLinks();
  brokenLinks.collectAnchor(field.name);

  return (
    <div
      className={`${styles.card} ${field.deprecated ? styles.cardDeprecated : ""}`}
      data-return={returnKind(field.type)}
    >
      <h3 id={field.name} className={styles.cardHeading}>
        <Signature field={field} />
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

        {field.notes.map((note, i) => (
          <p key={i} className={styles.note}>
            <MarkdownInline>{note}</MarkdownInline>
          </p>
        ))}

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
          <section className={styles.args} aria-label="Arguments">
            <dl className={styles.argList}>
              {field.args.map((arg) => (
                <div className={styles.arg} key={arg.name}>
                  <dt className={styles.argSignature}>
                    <code>
                      <span className={styles.argName}>{arg.name}</span>
                      <span className={styles.punct}>: </span>
                      <TypeRefView type={arg.type} />
                      {arg.defaultValue !== undefined && (
                        <span className={styles.literal}>
                          {" "}
                          = {arg.defaultValue}
                        </span>
                      )}
                    </code>
                  </dt>
                  {arg.description && (
                    <dd className={styles.argDesc}>
                      <Markdown>{arg.description}</Markdown>
                    </dd>
                  )}
                </div>
              ))}
            </dl>
          </section>
        )}
      </div>
    </div>
  );
}
