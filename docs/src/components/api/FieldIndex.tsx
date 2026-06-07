import React from "react";
import { returnKind, type ApiField, type ReturnKind } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

const RETURN_KIND_ORDER: Record<ReturnKind, number> = {
  scalar: 0,
  other: 1,
  same: 2,
};

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
  const indexedFields = fields
    .map((field, index) => ({
      field,
      index,
      kind: returnKind(field.type, ownerType),
    }))
    .sort(
      (a, b) =>
        RETURN_KIND_ORDER[a.kind] - RETURN_KIND_ORDER[b.kind] ||
        a.index - b.index
    );

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
