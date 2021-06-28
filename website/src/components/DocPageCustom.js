import React, { useState, useEffect } from 'react';
import qs from 'querystringify';
import isEmpty from 'lodash/isEmpty';
import { checkUserCollaboratorStatus } from '../api/github'
import Spinner from './Spinner';
import DocPageAuthentication from './DocPageAuthentication';
import DocPageRedirect from './DocPageRedirect';

function DocPageCustom({ location, userAccessStatus, setUserAccessStatus }) {
  const [isLoading, setIsLoading] = useState(true)
  const [redirectState, setRedirectState] = useState()
  const authQuery = qs.parse(location.search);

  useEffect(async () => {
    if (!isEmpty(authQuery) && userAccessStatus === null) { //callback after successful auth with github
      const user = await checkUserCollaboratorStatus(authQuery.code);
      setUserAccessStatus(user)
      if (user?.permission) {
        window.localStorage.setItem('user', JSON.stringify(user));
      }
    }
    setIsLoading(false)
  }, [])

  useEffect(() => {
    import('amplitude-js').then(amplitude => {
      if (userAccessStatus?.login) {
        var amplitudeInstance = amplitude.getInstance().init(process.env.REACT_APP_AMPLITUDE_ID, userAccessStatus?.login.toLowerCase(), {
          apiEndpoint: `${window.location.hostname}/t`
        });
        amplitude.getInstance().logEvent('Docs Viewed', { "hostname": window.location.hostname, "path": location.pathname });
      }
    })
  }, [location.pathname, userAccessStatus])

  if (isLoading) return <Spinner />

  if (userAccessStatus?.permission === false) {
    return <DocPageRedirect />
  }

  if (userAccessStatus === null) {
    return <DocPageAuthentication />
  }
}

export default DocPageCustom