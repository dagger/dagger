import React from "react";
import { useEnums } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

// EnumValues reveals the allowed values for an enum-typed argument, so a reader
// who sees `compression: ImageLayerCompression` can find out what to pass
// without hunting through the schema. Collapsed by default to keep dense arg
// tables scannable.
export default function EnumValues({ name }: { name: string }): JSX.Element | null {
  const enums = useEnums();
  const e = enums[name];
  if (!e) return null;
  return (
    <details className={styles.enum}>
      <summary>
        {e.values.length} value{e.values.length === 1 ? "" : "s"}
      </summary>
      <ul className={styles.enumList}>
        {e.values.map((v) => (
          <li key={v.name}>
            <code>{v.name}</code>
            {v.description && (
              <span className={styles.enumDesc}>
                {" — "}
                <MarkdownInline>{v.description}</MarkdownInline>
              </span>
            )}
          </li>
        ))}
      </ul>
    </details>
  );
}
