// THIS FILE WILL BE DEPRECATED SOON

import React from "react";
import styles from "../css/linkCTA.module.scss";
import Link from "@docusaurus/Link";

const LinkCTA = ({ url, label }) => (
  <Link className={styles.linkCTA} target="_self" href={url}>
    <img className="not-zoom" src="/img/current/sdk/cue/Dagger_Icons_Arrow-2-right.svg" />
    <span>{label}</span>
  </Link>
);

export default LinkCTA;
