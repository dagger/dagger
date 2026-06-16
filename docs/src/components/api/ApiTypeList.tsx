import React from "react";
import Link from "@docusaurus/Link";
import { useApiModel, useTypeHref } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

export default function ApiTypeList(): JSX.Element {
  const model = useApiModel();
  const typeHref = useTypeHref();

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
          {model.coreTypes.map((name) => {
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
