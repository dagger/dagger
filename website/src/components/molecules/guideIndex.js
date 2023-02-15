import React from "react";
import Link from "@docusaurus/Link";
import styles from "@site/src/css/molecules/guideIndex.module.scss";
import guidesJSON from "@site/static/guides.json";
import Tag from "../atoms/tag";

export default function GuidesIndex() {
  const guides = guidesJSON;

  return (
    <div className={styles.guideIndex}>
      <ul>
        {guides.map((x, i) => (
          <li key={i}>
            <GuideCard
              title={x.contentTitle}
              description={x.contentTitle}
              url={x.frontMatter.slug}
              tags={x.frontMatter.tags}
              authors={x.frontMatter.authors}
              date={x.frontMatter.date}
            />
          </li>
        ))}
      </ul>
    </div>
  );
}

function GuideCard({title, description, url, tags, authors, date}) {
  const handleAuthors = () => {
    let authorsString = "";
    authors.forEach((x) => (authorsString += `, ${x}`));
    return `By ${authorsString.slice(1)}`;
  };
  return (
    <div className={styles.guideCard}>
      <div className={styles.info}>
        <Link href={url}>
          <h3>{title}</h3>
        </Link>
        {date && <time>{date}</time>}
        {authors && <h4 className={styles.author}>{handleAuthors()}</h4>}
        {/* <p>{description}</p> */}
      </div>
      <div className={styles.tags}>
        {tags && tags.map((x, i) => <Tag key={i} label={x} />)}
      </div>
    </div>
  );
}
