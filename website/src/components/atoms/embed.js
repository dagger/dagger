import styles from "../../css/atoms/embed.module.scss";
import React, {useState} from "react";

const Embed = ({id, index}) => {
  const [loading, setLoading] = useState(true);

  return (
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
        loading={(index === 0 || !index) ? "eager" : "lazy"}
        src={`https://play.dagger.cloud/embed/${id}`}></iframe>
    </div>
  );
};

export default Embed;
