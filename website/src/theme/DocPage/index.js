/**
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */
import React, { useState, useEffect, useCallback } from 'react';
import { MDXProvider } from '@mdx-js/react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import renderRoutes from '@docusaurus/renderRoutes';
import Layout from '@theme/Layout';
import DocSidebar from '@theme/DocSidebar';
import MDXComponents from '@theme/MDXComponents';
import NotFound from '@theme/NotFound';
import IconArrow from '@theme/IconArrow';
import { matchPath } from '@docusaurus/router';
import { translate } from '@docusaurus/Translate';
import clsx from 'clsx';
import styles from './styles.module.css';
import { ThemeClassNames, docVersionSearchTag } from '@docusaurus/theme-common';
import { Redirect } from "react-router";
import qs from 'querystringify';
import isEmpty from 'lodash/isEmpty';
import { checkUserCollaboratorStatus } from '../../api/github'
import { GithubLoginButton } from 'react-social-login-buttons';
import Spinner from '../../components/Spinner';
import DocPageAuthentication from '../../components/DocPageAuthentication';
import DocPageRedirect from '../../components/DocPageRedirect';

function DocPageContent({ currentDocRoute, versionMetadata, children }) {
  const { siteConfig, isClient } = useDocusaurusContext();
  const { pluginId, permalinkToSidebar, docsSidebars, version } = versionMetadata;
  const sidebarName = permalinkToSidebar[currentDocRoute.path];
  const sidebar = docsSidebars[sidebarName];
  const [hiddenSidebarContainer, setHiddenSidebarContainer] = useState(false);
  const [hiddenSidebar, setHiddenSidebar] = useState(false);
  const toggleSidebar = useCallback(() => {
    if (hiddenSidebar) {
      setHiddenSidebar(false);
    }

    setHiddenSidebarContainer(!hiddenSidebarContainer);
  }, [hiddenSidebar]);

  return (
    <Layout
      key={isClient}
      wrapperClassName={ThemeClassNames.wrapper.docPages}
      pageClassName={ThemeClassNames.page.docPage}
      searchMetadatas={{
        version,
        tag: docVersionSearchTag(pluginId, version),
      }}>
      <div className={styles.docPage}>
        {sidebar && (
          <div
            className={clsx(styles.docSidebarContainer, {
              [styles.docSidebarContainerHidden]: hiddenSidebarContainer,
            })}
            onTransitionEnd={(e) => {
              if (
                !e.currentTarget.classList.contains(styles.docSidebarContainer)
              ) {
                return;
              }

              if (hiddenSidebarContainer) {
                setHiddenSidebar(true);
              }
            }}
            role="complementary">
            <DocSidebar
              key={
                // Reset sidebar state on sidebar changes
                // See https://github.com/facebook/docusaurus/issues/3414
                sidebarName
              }
              sidebar={sidebar}
              path={currentDocRoute.path}
              sidebarCollapsible={
                siteConfig.themeConfig?.sidebarCollapsible ?? true
              }
              onCollapse={toggleSidebar}
              isHidden={hiddenSidebar}
            />

            {hiddenSidebar && (
              <div
                className={styles.collapsedDocSidebar}
                title={translate({
                  id: 'theme.docs.sidebar.expandButtonTitle',
                  message: 'Expand sidebar',
                  description:
                    'The ARIA label and title attribute for expand button of doc sidebar',
                })}
                aria-label={translate({
                  id: 'theme.docs.sidebar.expandButtonAriaLabel',
                  message: 'Expand sidebar',
                  description:
                    'The ARIA label and title attribute for expand button of doc sidebar',
                })}
                tabIndex={0}
                role="button"
                onKeyDown={toggleSidebar}
                onClick={toggleSidebar}>
                <IconArrow className={styles.expandSidebarButtonIcon} />
              </div>
            )}
          </div>
        )}
        <main
          className={clsx(styles.docMainContainer, {
            [styles.docMainContainerEnhanced]:
              hiddenSidebarContainer || !sidebar,
          })}>
          <div
            className={clsx(
              'container padding-vert--lg',
              styles.docItemWrapper,
              {
                [styles.docItemWrapperEnhanced]: hiddenSidebarContainer,
              },
            )}>
            <MDXProvider components={MDXComponents}>{children}</MDXProvider>
          </div>
        </main>
      </div>
    </Layout>
  );
}

function DocPage(props) {
  const {
    route: { routes: docRoutes },
    versionMetadata,
    location,
  } = props;
  const currentDocRoute = docRoutes.find((docRoute) =>
    matchPath(location.pathname, docRoute),
  );

  // CUSTOM DOCPAGE
  if (process.env.OAUTH_ENABLE == 'true') {
    const [isLoading, setIsLoading] = useState(true)
    const [redirectState, setRedirectState] = useState()
    const authQuery = qs.parse(location.search);
    const [userAccessStatus, setUserAccessStatus] = useState((() => {
      if (typeof window !== "undefined") return JSON.parse(window.localStorage.getItem('user'))
    })())

    useEffect(async () => {
      if (!isEmpty(authQuery) && userAccessStatus === null) { //callback after successful auth with github
        const user = await checkUserCollaboratorStatus(authQuery.code);
        if (user?.permission) {
          setUserAccessStatus(user?.permission)
          if (typeof window !== "undefined") window.localStorage.setItem('user', JSON.stringify(user));
        }
      }
      setIsLoading(false)
    }, [userAccessStatus])


    if (isLoading) return <Spinner />

    if (userAccessStatus?.permission === false) {
      return <DocPageRedirect />
    }

    if (userAccessStatus === null) {
      return (
        <DocPageAuthentication />
      )
    }

    // TODO: DISABLE FOR LOCALHOST ENV
    import('amplitude-js').then(amplitude => {
      if (userAccessStatus?.login) {
        var amplitudeInstance = amplitude.getInstance().init(process.env.REACT_APP_AMPLITUDE_ID, userAccessStatus?.login.toLowerCase(), {
          apiEndpoint: `${window.location.hostname}/t`
        });
        amplitude.getInstance().logEvent('Docs View', { "path": window.location.pathname });
      }
    })
  }
  // END CUSTOM DOCPAGE

  if (!currentDocRoute) {
    return <NotFound {...props} />;
  }

  return (
    <DocPageContent
      currentDocRoute={currentDocRoute}
      versionMetadata={versionMetadata}>
      {renderRoutes(docRoutes)}
    </DocPageContent>
  );
}

export default DocPage;
