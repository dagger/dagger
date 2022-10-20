import React from "react";
import styles from "../../css/atoms/button.module.scss";
import Link from "@docusaurus/Link";

const Button = ({label, docPath, color}) => {
  return (
    <Link className={styles.anchor} href={`/${docPath}`} target="_self">
      <div
        className={`${styles.button} ${
          color === "light" ? styles.light : styles.dark
        }`}>
        {label}
      </div>
    </Link>
  );
};

export default Button;
