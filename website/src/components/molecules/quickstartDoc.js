import styles from "../../css/molecules/quickstartDoc.module.scss";
import React from "react";
import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

const QuickstartDoc = ({children, embeds}) => {
  return (
    <>
      <div className={styles.quickstartDoc}>
        <div className={styles.stepContent}>{children}</div>
        <div className={styles.stepEmbed}>
          <Tabs groupId="language">
            {Object.keys(embeds).map((x, index) => {
              return (
                <TabItem key={x} value={x}>
                  <iframe
                    loading={index === 0 ? "eager" : "lazy"}
                    src={`https://play.dagger.cloud/embed/${embeds[x]}`}></iframe>
                </TabItem>
              );
            })}
          </Tabs>
        </div>
      </div>
    </>
  );
};

export default QuickstartDoc;
