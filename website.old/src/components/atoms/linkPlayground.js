import React from "react";
import styles from "../../css/atoms/button.module.scss";
import Link from "@docusaurus/Link";

const LinkPlayground = ({url}) => {
  return (
    <Link className={styles.playground} href={`${url}`}>Try it in the API Playground!</Link>
  );
};

export default LinkPlayground;
