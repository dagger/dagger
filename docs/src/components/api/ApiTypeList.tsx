import React from "react";
import Link from "@docusaurus/Link";
import { useApiModel, useTypeHref } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

export default function ApiTypeList(): JSX.Element {
  const model = useApiModel();
  const typeHref = useTypeHref();
  const typeNames = [...model.coreTypes].sort((a, b) => a.localeCompare(b));

  return (
    <div className={`api-reference ${styles.apiType}`}>
      <table className={styles.typeList}>
        <thead>
          <tr>
            <th>Type</th>
            <th>Description</th>
          </tr>
        </thead>
        <tbody>
          {typeNames.map((name) => {
            const type = model.types[name];
            return (
              <tr key={name}>
                <td className={styles.typeListName}>
                  <Link to={typeHref(name)}>
                    <code>{name}</code>
                  </Link>
                </td>
                <td>
                  <MarkdownInline>
                    {type?.description || "No description."}
                  </MarkdownInline>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
