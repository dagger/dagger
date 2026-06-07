import React from "react";
import { returnKind, type ApiField } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

// FieldIndex is the quick-scan glossary shown before the cards on a long type —
// the same affordance as Dang's stdlib index (rendered past a threshold). It's
// a real <dl> dictionary (field name → one-line summary) so it sidesteps the
// site's global `.markdown table` styling and reads as plain prose.
export default function FieldIndex({
  fields,
  ownerType,
}: {
  fields: ApiField[];
  ownerType: string;
}): JSX.Element {
  return (
    <dl className={styles.index}>
      {fields.map((f) => (
        <React.Fragment key={f.name}>
          <dt
            className={styles.indexName}
            data-return={returnKind(f.type, ownerType)}
          >
            <a href={`#${f.name}`}>
              <code>{f.name}</code>
            </a>
          </dt>
          <dd className={styles.indexDesc}>
            <MarkdownInline>
              {f.description.split(/\n\s*\n/)[0] || ""}
            </MarkdownInline>
          </dd>
        </React.Fragment>
      ))}
    </dl>
  );
}
