import React from "react";
import Link from "@docusaurus/Link";
import styles from "@site/src/css/molecules/guideIndex.module.scss";
import guidesJSON from "@site/static/guides.json";
import Tag from "../atoms/tag";
import { DocSearch } from '@docsearch/react';

export default function GuidesIndex() {
  const guides = guidesJSON.guides;

  return (
    <div className={styles.guideIndex}>
      <div className={styles.search}>
        <label>
          This searchbar is guide focused. Here you can search all the guides by content.
        </label>
        <DocSearch
          apiKey="bffda1490c07dcce81a26a144115cc02"
          appId="XEIYPBWGOI"
          indexName="dagger"
          placeholder="Search in all guides"
          searchParameters={{ facetFilters: ["guide:true"] }}
        ></DocSearch>
      </div>
      <div>
      <ul>
        {guides.map((x, i) => (
          <li key={i}>
            <GuideCard
              title={x.contentTitle}
              url={x.frontMatter.slug}
              tags={x.frontMatter.tags}
              authors={x.frontMatter.authors}
              timestamp={x.timestamp}
            />
          </li>
        ))}
      </ul>
      </div>
    </div>
  );
}

function GuideCard({title, url, tags, authors, timestamp}) {
  const handleAuthors = () => {
    let authorsString = "";
    authors.forEach((x) => (authorsString += `, ${x}`));
    return `By ${authorsString.slice(1)}`;
  };

  const dateOptions = {year: "numeric", month: "long", day: "numeric"};
  const date = new Date(timestamp).toLocaleDateString("en-US", dateOptions);

  return (
    <div className={styles.guideCard}>
      <div className={styles.info}>
        <Link href={url}>
          <h3>{title}</h3>
        </Link>
        {date && <time>{date}</time>}
        {authors && <h4 className={styles.author}>{handleAuthors()}</h4>}
      </div>
      <div className={styles.tags}>
        {tags &&
          tags.map((x, i) => (
            <Tag key={i} label={x} />
          ))}
      </div>
    </div>
  );
}
