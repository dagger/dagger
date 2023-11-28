import styles from "../../css/atoms/embed.module.scss";
import React, {useState} from "react";
import BrowserOnly from "@docusaurus/BrowserOnly";

const Embed = ({id, index}) => {
  const [loading, setLoading] = useState(true);

  return (
    <BrowserOnly>
      {() => (
        <div className={styles.embedWrapper} id="embedWrapper">
          {loading && (
            <div className={styles.spinnerWrapper}>
              <div className={styles.spinner}></div>
            </div>
          )}
          <iframe
            className={styles.embed}
            onLoad={() => setLoading(false)}
            style={{display: loading ? "hidden" : "inherit"}}
            loading={index === 0 || !index ? "eager" : "lazy"}
            src={`${
              window.location.origin.includes("localhost")
                ? "https://play.dagger.cloud"
                : window.location.origin
            }/embed/${id}`}></iframe>
        </div>
      )}
    </BrowserOnly>
  );
};

export default Embed;
