import React, { useEffect, useRef } from "react";
import { useEnums } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

// EnumValues reveals the allowed values for an enum-typed argument, so a reader
// who sees `compression: ImageLayerCompression` can find out what to pass
// without hunting through the schema. Collapsed by default to keep dense arg
// blocks scannable.
export default function EnumValues({ name }: { name: string }): JSX.Element | null {
  const detailsRef = useRef<HTMLDetailsElement>(null);
  const enums = useEnums();
  const e = enums[name];
  useEffect(() => {
    const closeIfOutside = (event: PointerEvent) => {
      const details = detailsRef.current;
      if (!details?.open) return;
      if (event.target instanceof Node && details.contains(event.target)) return;
      details.open = false;
    };

    document.addEventListener("pointerdown", closeIfOutside);
    return () => document.removeEventListener("pointerdown", closeIfOutside);
  }, []);

  if (!e) return null;
  return (
    <details className={styles.enum} ref={detailsRef}>
      <summary>
        {e.values.length} option{e.values.length === 1 ? "" : "s"}
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
