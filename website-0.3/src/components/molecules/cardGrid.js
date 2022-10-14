import React from "react";
import styles from "../../css/molecules/cardGrid.module.scss";
import Card from "./card";

const CardGrid = ({data}) => {
  return (
    <div className={styles.cardGrid}>
      {data.map((x, index) => (
        <Card
          key={index}
          label={x.label}
          description={x.description}
          relatedContent={x.relatedContent}
          icon={x.imgFilename}
          url={x.url}
        />
      ))}
    </div>
  );
};

export default CardGrid;
