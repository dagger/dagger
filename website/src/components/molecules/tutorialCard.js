import React from "react";
import styles from "../../css/molecules/tutorialCard.module.scss";
import Link from "@docusaurus/Link";
import Button from "../atoms/button";

const TutorialCard = ({img, title, description, buttonLabel, url}) => {
  return (
    <Link href="/1200/local-dev" className={styles.tutorialCard}>
      <img src={img}></img>
      <div>
        <h4>{title}</h4>
        <p>{description}</p>
        {buttonLabel ? (
          <Button label={buttonLabel} docPath={url} color="light" />
        ) : (
          ""
        )}
      </div>
    </Link>
  );
};

export default TutorialCard;
