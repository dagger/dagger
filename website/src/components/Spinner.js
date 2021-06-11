import React from 'react';
import styles from './Spinner.module.css';

export default function Spinner() {
    return (
        <div className={styles.ellipsis}><div></div><div></div><div></div><div></div></div>
    )
}