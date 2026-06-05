import React from "react";
import { useApiType } from "./data";
import Markdown from "./Markdown";
import FieldIndex from "./FieldIndex";
import FieldCard from "./FieldCard";
import References from "./References";
import styles from "./styles.module.scss";

// Past this many fields we show a quick-scan index before the cards, mirroring
// Dang's stdlib indexThreshold. Short types read fine as bare cards.
const INDEX_THRESHOLD = 8;

// Core interfaces worth linking from the "implements" line. Node/Exportable/
// Syncer are the recurring ones; we link any we can resolve and show the rest
// as plain text.
function ImplementsLine({ names }: { names: string[] }): JSX.Element | null {
  if (names.length === 0) return null;
  return (
    <p className={styles.implements}>
      Implements{" "}
      {names.map((n, i) => (
        <React.Fragment key={n}>
          {i > 0 && ", "}
          <code>{n}</code>
        </React.Fragment>
      ))}
    </p>
  );
}

/**
 * ApiType renders the full, schema-generated reference for one core type:
 * its description, the interfaces it implements, a quick-scan field index,
 * and an anchored card per field. Drop `<ApiType name="Container" />` into an
 * MDX page and the content comes straight from docs-graphql/schema.graphqls.
 */
export default function ApiType({ name }: { name: string }): JSX.Element {
  const type = useApiType(name);
  return (
    <div className={`api-reference ${styles.apiType}`}>
      <Markdown className={styles.typeDesc}>{type.description}</Markdown>
      <ImplementsLine names={type.implements} />

      {type.fields.length > INDEX_THRESHOLD && (
        <FieldIndex fields={type.fields} />
      )}

      <div className={styles.fields}>
        {type.fields.map((f) => (
          <FieldCard key={f.name} field={f} />
        ))}
      </div>

      <References returnedBy={type.returnedBy} argOf={type.argOf} />
    </div>
  );
}
