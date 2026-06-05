import React from "react";
import type { ApiField } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

// FieldIndex is the quick-scan table shown before the cards on a long type —
// the same affordance as Dang's stdlib index (rendered past a threshold). It
// lets a reader jump straight to a field by name.
export default function FieldIndex({
  fields,
}: {
  fields: ApiField[];
}): JSX.Element {
  return (
    <table className={styles.index}>
      <tbody>
        {fields.map((f) => (
          <tr key={f.name}>
            <td className={styles.indexName}>
              <a href={`#${f.name}`}>
                <code>{f.name}</code>
              </a>
            </td>
            <td className={styles.indexDesc}>
              <MarkdownInline>
                {f.description.split(/\n\s*\n/)[0] || ""}
              </MarkdownInline>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
