import React from "react";
import Link from "@docusaurus/Link";
import { useApiModel, useApiType, typeHref } from "./data";
import Markdown from "./Markdown";
import FieldIndex from "./FieldIndex";
import FieldCard from "./FieldCard";
import References from "./References";
import styles from "./styles.module.scss";

// Past this many fields we show a quick-scan index before the cards, mirroring
// Dang's stdlib indexThreshold. Short types read fine as bare cards.
const INDEX_THRESHOLD = 8;

function ImplementsLine({ names }: { names: string[] }): JSX.Element | null {
  const published = new Set(useApiModel().coreTypes);
  if (names.length === 0) return null;
  return (
    <p className={styles.implements}>
      Implements{" "}
      {names.map((n, i) => (
        <React.Fragment key={n}>
          {i > 0 && ", "}
          {published.has(n) ? (
            <Link to={typeHref(n)}>
              <code>{n}</code>
            </Link>
          ) : (
            <code>{n}</code>
          )}
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
export default function ApiType({
  name,
  showDescription = true,
}: {
  name: string;
  showDescription?: boolean;
}): JSX.Element {
  const type = useApiType(name);
  return (
    <div className={`api-reference ${styles.apiType}`}>
      {showDescription && (
        <Markdown className={styles.typeDesc}>{type.description}</Markdown>
      )}
      <ImplementsLine names={type.implements} />

      {type.fields.length > INDEX_THRESHOLD && (
        <FieldIndex fields={type.fields} ownerType={type.name} />
      )}

      <div className={styles.fields}>
        {type.fields.map((f) => (
          <FieldCard key={f.name} field={f} ownerType={type.name} />
        ))}
      </div>

      <References returnedBy={type.returnedBy} argOf={type.argOf} />
    </div>
  );
}
