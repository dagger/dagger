import React from "react";
import { orderedApiFields, returnKind, type ApiField } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

// FieldIndex is the quick-scan glossary shown before the cards on a long type —
// the same affordance as Dang's stdlib index (rendered past a threshold). It's
// a real <dl> dictionary (field name → one-line summary) so it sidesteps the
// site's global `.markdown table` styling and reads as plain prose.
export default function FieldIndex({
  fields,
}: {
  fields: ApiField[];
}): JSX.Element {
  const indexedFields = orderedApiFields(fields).map((field) => ({
    field,
    kind: returnKind(field.type),
  }));

  return (
    <dl className={styles.index}>
      {indexedFields.map(({ field, kind }, i) => {
        const startsGroup = i > 0 && indexedFields[i - 1].kind !== kind;
        return (
          <React.Fragment key={field.name}>
            <dt
              className={styles.indexName}
              data-return={kind}
              data-group-start={startsGroup ? "true" : undefined}
            >
              <a href={`#${field.name}`}>
                <code>{field.name}</code>
              </a>
            </dt>
            <dd
              className={styles.indexDesc}
              data-group-start={startsGroup ? "true" : undefined}
            >
              <MarkdownInline>
                {field.description.split(/\n\s*\n/)[0] || ""}
              </MarkdownInline>
            </dd>
          </React.Fragment>
        );
      })}
    </dl>
  );
}
