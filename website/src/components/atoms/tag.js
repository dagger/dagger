import React from "react";
import styles from "@site/src/css/molecules/tag.module.scss"

export default function Tag({label}) {
    return (
        <span className={styles.tag}>{label}</span>
    )
}