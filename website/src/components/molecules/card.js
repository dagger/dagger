import React from "react";
import styles from "../../css/molecules/card.module.scss";
import Link from "@docusaurus/Link";
import LinkCTA from "../atoms/linkCTA";

const Card = ({label, icon, description, relatedContent, fullWidth, url}) =>
  !url ? (
    <div className={`${styles.card} ${fullWidth ? styles.fullWidth : ""}`}>
      <img src={`/img/${icon}`}></img>
      <div className={styles.description}>
        <h4>{label}</h4>
        <p>{description}</p>
      </div>
      {relatedContent ? (
        <>
          <hr></hr>
          <RelatedContent content={relatedContent} />
        </>
      ) : (
        ""
      )}
    </div>
  ) : (
    <Link href={url} style={{textDecoration: "none"}} className={styles.link}>
      <div className={`${styles.card} ${fullWidth ? styles.fullWidth : ""}`}>
        <img src={`/img/${icon}`}></img>
        <div className={styles.description}>
          <h4>{label}</h4>
          <p>{description}</p>
        </div>
      </div>
    </Link>
  );

const RelatedContent = ({content}) => {
  return (
    <div className={styles.relatedContent}>
      <ul>
        {content.map((x, index) => (
          <li className={styles.item} key={index}>
            <span>
              <img src="/img/Dagger_Icons_Doc.svg" />
              <Link href={x.url} target="_blank">
                {x.label}
              </Link>
            </span>
          </li>
        ))}
      </ul>
      <LinkCTA url="/1220/vs" label={`See All ${content.length}`} />
    </div>
  );
};

export default Card;
