import styles from "../css/quickstartDoc.module.scss";
import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";
import Embed from "./embed";
import { HtmlClassNameProvider } from "@docusaurus/theme-common";
import React from "react";

/**
 * This component is used to render the quickstart documentation
 * pages that have an embedded iframe.
 *
 * It creates a two column layout with the first column containing
 * the documentation and the second column containing the iframe.
 *
 * The iframe is rendered using the Embed component.
 *
 * The embeds prop is an object that contains the language and the
 * id of the Playground snippet to render.
 *
 * @param children
 * @param embeds
 * @returns
 */
const QuickstartDoc = ({children, embeds}) => {
  return (
    <HtmlClassNameProvider className="quickstart-page-with-embed">
      <div className={styles.quickstartDoc}>
        <div className={styles.stepContent}>{children}</div>
        <div className={styles.stepEmbed}>
          <Tabs groupId="language">
            {Object.keys(embeds).map((x, index) => {
              return (
                <TabItem key={x} value={x}>
                  <Embed id={embeds[x]} index={index} />
                </TabItem>
              );
            })}
          </Tabs>
        </div>
      </div>
    </HtmlClassNameProvider>
  );
};

export default QuickstartDoc;
