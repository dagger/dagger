import styles from "../../css/molecules/quickstartDoc.module.scss";
import React, {useEffect, useState} from "react";
import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

const QuickstartDoc = ({children, embeds}) => {
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    console.log(loading);
  }, [loading]);

  return (
    <>
      <div className={styles.quickstartDoc}>
        <div className={styles.stepContent}>{children}</div>
        <div className={styles.stepEmbed}>
          <Tabs groupId="language">
            {Object.keys(embeds).map((x, index) => {
              return (
                <TabItem key={x} value={x}>
                  {loading ? (
                    <div className={styles.spinnerWrapper}>
                      <div className={styles.spinner}></div>
                    </div>
                  ) : null}
                  <iframe
                    onLoad={() => setLoading(false)}
                    style={{display: loading ? "hidden" : "inherit"}}
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
