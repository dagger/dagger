import React, { type ReactNode } from "react";
import clsx from "clsx";
import Link from "@docusaurus/Link";
import { useDoc } from "@docusaurus/plugin-content-docs/client";
import { ThemeClassNames, useWindowSize } from "@docusaurus/theme-common";
import ContentVisibility from "@theme/ContentVisibility";
import DocBreadcrumbs from "@theme/DocBreadcrumbs";
import DocItemContent from "@theme/DocItem/Content";
import DocItemFooter from "@theme/DocItem/Footer";
import DocItemPaginator from "@theme/DocItem/Paginator";
import DocItemTOCMobile from "@theme/DocItem/TOC/Mobile";
import DocVersionBadge from "@theme/DocVersionBadge";
import DocVersionBanner from "@theme/DocVersionBanner";
import NavbarColorModeToggle from "@theme/Navbar/ColorModeToggle";
import TOC from "@theme/TOC";
import type { Props } from "@theme/DocItem/Layout";

import styles from "./styles.module.css";

function useDocTOC() {
  const { frontMatter, toc } = useDoc();
  const windowSize = useWindowSize();

  const hidden = frontMatter.hide_table_of_contents;
  const canRender = !hidden && toc.length > 0;

  const mobile = canRender ? <DocItemTOCMobile /> : undefined;

  const desktop =
    !hidden && (windowSize === "desktop" || windowSize === "ssr") ? (
      <DocRightSidebar showToc={canRender} />
    ) : undefined;

  return {
    hidden,
    mobile,
    desktop,
  };
}

function DocRightSidebar({ showToc }: { showToc: boolean }): JSX.Element {
  const { frontMatter, toc } = useDoc();

  return (
    <aside className={styles.rightSidebar}>
      <div className={styles.rightSidebarControls} aria-label="Page actions">
        <Link
          className={clsx(
            "dagger-cloud-button docs-right-sidebar-cloud-button",
            styles.cloudButton
          )}
          to="https://dagger.io/cloud"
          target="_blank"
          rel="noopener noreferrer"
        >
          Try Dagger Cloud
        </Link>
        <NavbarColorModeToggle className={styles.colorModeToggle} />
      </div>
      {showToc && (
        <TOC
          toc={toc}
          minHeadingLevel={frontMatter.toc_min_heading_level}
          maxHeadingLevel={frontMatter.toc_max_heading_level}
          className={ThemeClassNames.docs.docTocDesktop}
        />
      )}
    </aside>
  );
}

export default function DocItemLayout({ children }: Props): ReactNode {
  const docTOC = useDocTOC();
  const { metadata } = useDoc();

  return (
    <div className="row">
      <div className={clsx("col", !docTOC.hidden && styles.docItemCol)}>
        <ContentVisibility metadata={metadata} />
        <DocVersionBanner />
        <div className={styles.docItemContainer}>
          <article>
            <DocBreadcrumbs />
            <DocVersionBadge />
            {docTOC.mobile}
            <DocItemContent>{children}</DocItemContent>
            <DocItemFooter />
          </article>
          <DocItemPaginator />
        </div>
      </div>
      {docTOC.desktop && <div className="col col--3">{docTOC.desktop}</div>}
    </div>
  );
}
