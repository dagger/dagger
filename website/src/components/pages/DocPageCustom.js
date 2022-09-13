import React, { useState, useEffect } from 'react';
import qs from 'querystringify';
import isEmpty from 'lodash/isEmpty';
import NProgress from "nprogress";

import { checkUserCollaboratorStatus } from '../../api/github'
import DocPageAuthentication from './DocPageAuthentication';
import DocPageRedirect from './DocPageRedirect';

function DocPageCustom({ location, userAccessStatus, setUserAccessStatus }) {
  const [isLoading, setIsLoading] = useState(true)
  const [redirectState, setRedirectState] = useState()
  const authQuery = qs.parse(location.search);

  useEffect(async () => {
    try {
      NProgress.start()
      if (!isEmpty(authQuery?.code) && userAccessStatus === null) { //callback after successful auth with github
        const user = await checkUserCollaboratorStatus(authQuery?.code);
        setUserAccessStatus(user)
        if (user?.permission) {
          window.localStorage.setItem('user', JSON.stringify(user));
        }
      }
      NProgress.done();
      setIsLoading(false)
    } catch(error) {
      console.log(error)
    }
  }, [])

  if(isLoading) return <p>...</p>


  if (userAccessStatus?.permission === false) {
    return <DocPageRedirect />
  }

  if (userAccessStatus === null) {
    return <DocPageAuthentication />
  }

  return null
}

export default DocPageCustom