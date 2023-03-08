import styles from "../../css/molecules/quickstartDoc.module.scss";
import React from "react";
import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";
import Embed from "../atoms/embed";

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
                  <Embed id={embeds[x]} index={index}/>
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
