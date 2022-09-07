import React, { useEffect, useState } from 'react';
import clsx from 'clsx';
import {HtmlClassNameProvider, ThemeClassNames} from '@docusaurus/theme-common';
import {
  docVersionSearchTag,
  DocsSidebarProvider,
  DocsVersionProvider,
  useDocRouteMetadata,
} from '@docusaurus/theme-common/internal';
import DocPageLayout from '@theme/DocPage/Layout';
import NotFound from '@theme/NotFound';
import SearchMetadata from '@theme/SearchMetadata';

import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {useLocation} from '@docusaurus/router';
import DocPageCustom from "../../components/pages/DocPageCustom"
import amplitude from 'amplitude-js';

export default function DocPage(props) {
  const {versionMetadata} = props;
  const currentDocRouteMetadata = useDocRouteMetadata(props);
  
  // DocPage Swizzle
  const {siteConfig} = useDocusaurusContext();
  const location = useLocation();
  const [userAccessStatus, setUserAccessStatus] = useState(
    (() => {
      if (typeof window !== 'undefined')
        return JSON.parse(window.localStorage.getItem('user'));
    })(),
  );
  
  useEffect(() => {
      if(siteConfig.customFields.AMPLITUDE_ID) {
        var instance1 = amplitude.getInstance().init(siteConfig.customFields.AMPLITUDE_ID, null, {
          apiEndpoint: `${window.location.hostname}/t`
        })
        amplitude.getInstance().logEvent('Docs Viewed', { "hostname": window.location.hostname, "path": location.pathname });
      }
  }, [location.pathname])

  if (siteConfig.customFields.OAUTH_ENABLE == 'true' && userAccessStatus?.permission !== true) {
    return <DocPageCustom location={location} userAccessStatus={userAccessStatus} setUserAccessStatus={setUserAccessStatus} />
  }
  // End DocPageSwizzle

  if (!currentDocRouteMetadata) {
    return <NotFound />;
  }
  const {docElement, sidebarName, sidebarItems} = currentDocRouteMetadata;
  return (
    <>
      <SearchMetadata
        version={versionMetadata.version}
        tag={docVersionSearchTag(
          versionMetadata.pluginId,
          versionMetadata.version,
        )}
      />
      <HtmlClassNameProvider
        className={clsx(
          // TODO: it should be removed from here
          ThemeClassNames.wrapper.docsPages,
          ThemeClassNames.page.docsDocPage,
          props.versionMetadata.className,
        )}>
        <DocsVersionProvider version={versionMetadata}>
          <DocsSidebarProvider name={sidebarName} items={sidebarItems}>
            <DocPageLayout>{docElement}</DocPageLayout>
          </DocsSidebarProvider>
        </DocsVersionProvider>
      </HtmlClassNameProvider>
    </>
  );
}
