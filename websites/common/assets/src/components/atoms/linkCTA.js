import React from "react";
import styles from "../../css/atoms/linkCTA.module.scss";
import Link from "@docusaurus/Link";

const LinkCTA = ({ url, label }) => (
  <Link className={styles.linkCTA} target="_self" href={url}>
    <img className="not-zoom" src="/img/Dagger_Icons_Arrow-2-right.svg" />
    <span>{label}</span>
  </Link>
);

export default LinkCTA;
