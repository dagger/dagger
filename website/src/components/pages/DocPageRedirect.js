import React, { useEffect, useState } from "react";
import style from '../../css/pages/DocPageRedirect.module.css'

import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

export default function DocPageRedirect() {
    const [counter, setCounter] = useState(10)
    const {siteConfig} = useDocusaurusContext();

    useEffect(() => {
        setTimeout(() => window.location.href = siteConfig.customFields.REACT_APP_DAGGER_SITE_URI, 10000)
        setInterval(() => setCounter((prevState) => prevState - 1), 1000)
    }, [])

    return (
        <div data-cy="cy-page-redirect" className={`container ${style.wrapper}`}>
            <div className={`row ${style.row}`}>
                <div className="col col--4 col--offset-2">
                    <h1 className={style.h1}>Oups!</h1>
                    <p>It seems you don't have the permission to see Dagger's documentation. But don't worry you can request an Eary Access :). You'll be redirect to Dagger website in {counter} seconds </p>
                    <p>See you soon !</p>
                    <br />
                    <small><strong>If nothing happen, <a href={siteConfig.customFields.REACT_APP_DAGGER_SITE_URI}>click here</a> to go to Dagger website</strong></small>
                </div>
                <div className="col col--4">
                    <img src="/img/dagger-astronaute.png" alt="" />
                </div>
            </div>
        </div>
    )
}