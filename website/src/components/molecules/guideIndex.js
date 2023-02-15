import React from "react";
import Link from "@docusaurus/Link";
import guidesJSON from "@site/static/guides.json";

export default function GuidesIndex() {
  const guides = guidesJSON;

  return (
    <div>
      <h2>Index</h2>
      <ul>
        {guides.map((x, i) => (
          <li key={i}>
            <div>
                <Link href={x.frontMatter.slug}>{x.contentTitle}</Link>
                {x.frontMatter.tags.map((x, i) => (<Tag label={x} key={i}/>))}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Tag({label}) {
  return <span>{label}</span>;
}
