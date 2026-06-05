import React from "react";
import Link from "@docusaurus/Link";
import type { FieldRef } from "./data";
import { useApiModel, typeHref } from "./data";
import styles from "./styles.module.scss";

// A single "Type.field" reference, linked to that field's anchor when the
// source type is itself a published reference page.
function Ref({ r, core }: { r: FieldRef; core: Set<string> }): JSX.Element {
  const label = `${r.type}.${r.field}`;
  const body = (
    <code>
      {r.type}.<span className={styles.refField}>{r.field}</span>
      {r.arg && <span className={styles.refArg}> ({r.arg})</span>}
    </code>
  );
  if (core.has(r.type)) {
    return (
      <Link className={styles.ref} to={`${typeHref(r.type)}#${r.field}`}>
        {body}
      </Link>
    );
  }
  return (
    <span className={styles.ref} title={label}>
      {body}
    </span>
  );
}

function RefList({ refs, core }: { refs: FieldRef[]; core: Set<string> }) {
  return (
    <div className={styles.refList}>
      {refs.map((r) => (
        <Ref key={`${r.type}.${r.field}.${r.arg ?? ""}`} r={r} core={core} />
      ))}
    </div>
  );
}

// References surfaces where a type shows up elsewhere in the API: the fields
// that return it and the fields that take it as an argument. It's derived from
// the whole schema, so it's the kind of map a reader can't easily build by
// hand — a strong signal of how a type connects to the rest of Dagger.
export default function References({
  returnedBy,
  argOf,
}: {
  returnedBy: FieldRef[];
  argOf: FieldRef[];
}): JSX.Element | null {
  const core = new Set(useApiModel().coreTypes);
  if (returnedBy.length === 0 && argOf.length === 0) return null;
  return (
    <section className={styles.references}>
      <h2>References</h2>
      {returnedBy.length > 0 && (
        <>
          <h3 className={styles.refHeading}>Returned by</h3>
          <RefList refs={returnedBy} core={core} />
        </>
      )}
      {argOf.length > 0 && (
        <>
          <h3 className={styles.refHeading}>Accepted as an argument by</h3>
          <RefList refs={argOf} core={core} />
        </>
      )}
    </section>
  );
}
