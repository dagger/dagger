import React, {useEffect} from 'react';
import NotFound from '@theme/NotFound';
import DocPageLayout from '@theme/DocPage/Layout';
import clsx from 'clsx';
import {
  HtmlClassNameProvider,
  ThemeClassNames,
  docVersionSearchTag,
  DocsSidebarProvider,
  DocsVersionProvider,
  useDocRouteMetadata,
} from '@docusaurus/theme-common';
import SearchMetadata from '@theme/SearchMetadata';
import amplitude from 'amplitude-js';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {useLocation} from '@docusaurus/router';

export default function DocPage(props) {
  const {versionMetadata} = props;
  const currentDocRouteMetadata = useDocRouteMetadata(props);

    // DocPage Swizzle
  const {siteConfig} = useDocusaurusContext();
  const location = useLocation();
  
  useEffect(() => {
      if(siteConfig.customFields.AMPLITUDE_ID) {
        var instance1 = amplitude.getInstance().init(siteConfig.customFields.AMPLITUDE_ID, null, {
          apiEndpoint: `${window.location.hostname}/t`
        })
        amplitude.getInstance().logEvent('Docs Viewed', { "hostname": window.location.hostname, "path": location.pathname });
      }
  }, [location.pathname])
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
