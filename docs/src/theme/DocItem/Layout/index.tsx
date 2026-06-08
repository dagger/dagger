import React, { type ReactNode } from "react";
import clsx from "clsx";
import Link from "@docusaurus/Link";
import { useLocation } from "@docusaurus/router";
import type { TOCItem } from "@docusaurus/mdx-loader";
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
import { typeSlug, useApiModel } from "@site/src/components/api/data";

import styles from "./styles.module.css";

type DocTOC = {
  toc: readonly TOCItem[];
  minHeadingLevel?: number;
  maxHeadingLevel?: number;
};

const DEFAULT_MIN_HEADING_LEVEL = 2;
const DEFAULT_MAX_HEADING_LEVEL = 3;

function normalizePath(pathname: string): string {
  return pathname.replace(/\/+$/, "");
}

function useApiReferenceTOC(
  toc: readonly TOCItem[],
  minHeadingLevel?: number,
  maxHeadingLevel?: number
): DocTOC {
  const model = useApiModel();
  const { pathname } = useLocation();
  const resolvedMin = minHeadingLevel ?? DEFAULT_MIN_HEADING_LEVEL;
  const resolvedMax = maxHeadingLevel ?? DEFAULT_MAX_HEADING_LEVEL;

  return React.useMemo(() => {
    const filteredToc = toc.filter(
      (item) => item.level >= resolvedMin && item.level <= resolvedMax
    );
    const path = normalizePath(pathname);
    const currentType = Object.values(model.types).find((type) =>
      path.endsWith(`/extending/types/${typeSlug(type.name)}`)
    );
    if (!currentType) {
      return { toc: filteredToc, minHeadingLevel, maxHeadingLevel };
    }

    const apiReferenceIndex = filteredToc.findIndex(
      (item) => item.id === "api-reference"
    );
    if (apiReferenceIndex === -1 || currentType.fields.length === 0) {
      return { toc: filteredToc, minHeadingLevel, maxHeadingLevel };
    }

    const apiReferenceLevel = filteredToc[apiReferenceIndex].level;
    const fieldLevel = apiReferenceLevel + 1;
    const fieldItems: TOCItem[] = currentType.fields.map((field) => ({
      value: field.name,
      id: field.name,
      level: fieldLevel,
    }));

    return {
      toc: [
        ...filteredToc.slice(0, apiReferenceIndex + 1),
        ...fieldItems,
        ...filteredToc.slice(apiReferenceIndex + 1),
      ],
      minHeadingLevel,
      maxHeadingLevel: Math.max(resolvedMax, fieldLevel),
    };
  }, [
    maxHeadingLevel,
    minHeadingLevel,
    model.types,
    pathname,
    resolvedMax,
    resolvedMin,
    toc,
  ]);
}

function useDocTOC() {
  const { frontMatter, toc } = useDoc();
  const windowSize = useWindowSize();

  const hidden = frontMatter.hide_table_of_contents;
  const desktopToc = useApiReferenceTOC(
    toc,
    frontMatter.toc_min_heading_level,
    frontMatter.toc_max_heading_level
  );
  const canRender = !hidden && toc.length > 0;
  const canRenderDesktop = !hidden && desktopToc.toc.length > 0;

  const mobile = canRender ? <DocItemTOCMobile /> : undefined;

  const desktop =
    !hidden && (windowSize === "desktop" || windowSize === "ssr") ? (
      <DocRightSidebar showToc={canRenderDesktop} docTOC={desktopToc} />
    ) : undefined;

  return {
    hidden,
    mobile,
    desktop,
  };
}

function DocRightSidebar({
  showToc,
  docTOC,
}: {
  showToc: boolean;
  docTOC: DocTOC;
}): JSX.Element {
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
          toc={docTOC.toc}
          minHeadingLevel={docTOC.minHeadingLevel}
          maxHeadingLevel={docTOC.maxHeadingLevel}
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
