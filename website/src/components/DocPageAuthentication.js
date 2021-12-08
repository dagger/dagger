import React from "react";
import { GithubLoginButton } from 'react-social-login-buttons';
import style from './DocPageAuthentication.module.css'

export default function DocAuthentication() {
    return (
        <div data-cy="cy-signin" className={style.container}>
            <h1 className={style.h1}>Welcome to the Dagger documentation</h1>
            <p>Please Sign In to Github to get access to the doc</p>
            <div data-cy="cy-btn-signin">
                <GithubLoginButton className={style.btn__github} onClick={() => window.location.href = process.env.REACT_APP_GITHUB_AUTHORIZE_URI} />
            </div>
        </div>
    )
}