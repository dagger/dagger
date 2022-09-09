import React from "react";
import styles from "../../css/molecules/tutorialCard.module.scss";
import Link from "@docusaurus/Link";
import Button from "../atoms/button";

const TutorialCard = ({ img, title, description, buttonLabel, url }) => {
  return (
    <div className={styles.tutorialCard}>
      <img src={img} className="not-zoom"></img>
      <div>
        <h4>{title}</h4>
        <p>{description}</p>
        {buttonLabel ? (
          <Button label={buttonLabel} docPath={url} color="light" />
        ) : (
          ""
        )}
      </div>
    </div>
  );
};

export default TutorialCard;
