import React from "react";
import styles from "@site/src/css/molecules/tag.module.scss";

export default function Tag({label, onTagClick, onCloseClick, removable}) {
  return (
    <div onClick={onTagClick} className={styles.tag} style={{cursor: removable ? "initial" : "pointer"}}>
      <span>{label}</span>
      {removable && (
        <div className={styles.close} onClick={onCloseClick}>
          <svg viewBox="0 0 15 15" width="5" height="5">
            <g stroke="black" strokeWidth="3">
              <path d="M.75.75l13.5 13.5M14.25.75L.75 14.25" />
            </g>
          </svg>
        </div>
      )}
    </div>
  );
}
