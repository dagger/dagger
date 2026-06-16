import React from "react";
import clsx from "clsx";
import useDocusaurusContext from "@docusaurus/useDocusaurusContext";
import { useLocation } from "@docusaurus/router";
import { useThemeConfig } from "@docusaurus/theme-common";
import Logo from "@theme/Logo";
import SearchBar from "@theme/SearchBar";
import CollapseButton from "@theme/DocSidebar/Desktop/CollapseButton";
import Content from "@theme/DocSidebar/Desktop/Content";
import type { Props } from "@theme/DocSidebar/Desktop";

import styles from "./styles.module.css";

const versions = require("@site/versions.json") as string[];
const latestVersion = versions[0];
const currentVersionLabel = "Next";

function joinBaseUrl(baseUrl: string, path: string): string {
  const normalizedBase = baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`;
  return `${normalizedBase}${path.replace(/^\/+/, "")}`;
}

function getCurrentVersion(pathname: string, baseUrl: string): string {
  const normalizedBase = baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`;
  const pathWithoutBase = pathname.startsWith(normalizedBase)
    ? pathname.slice(normalizedBase.length)
    : pathname.replace(/^\/+/, "");

  if (pathWithoutBase === "next" || pathWithoutBase.startsWith("next/")) {
    return "current";
  }

  return (
    versions.find(
      (version) =>
        pathWithoutBase === version || pathWithoutBase.startsWith(`${version}/`)
    ) ?? latestVersion
  );
}

function DocsVersionSelect() {
  const {
    siteConfig: { baseUrl },
  } = useDocusaurusContext();
  const { pathname } = useLocation();
  const currentVersion = getCurrentVersion(pathname, baseUrl);

  return (
    <select
      aria-label="Docs version"
      className={styles.versionSelect}
      value={currentVersion}
      onChange={(event) => {
        const nextValue = event.currentTarget.value;
        const nextPath =
          nextValue === "current"
            ? joinBaseUrl(baseUrl, "next/")
            : nextValue === latestVersion
              ? baseUrl
              : joinBaseUrl(baseUrl, `${nextValue}/`);

        window.location.href = nextPath;
      }}
    >
      {versions.map((version) => (
        <option key={version} value={version}>
          {version}
        </option>
      ))}
      <option value="current">{currentVersionLabel}</option>
    </select>
  );
}

function scrollActiveSidebarItemIntoView(sidebar: HTMLElement) {
  const activeLink =
    sidebar.querySelector<HTMLElement>(
      '.menu__link--active[aria-current="page"]'
    ) ?? sidebar.querySelector<HTMLElement>(".menu__link--active");
  const scroller =
    activeLink?.closest<HTMLElement>(".menu") ??
    sidebar.querySelector<HTMLElement>(".menu");

  if (!activeLink || !scroller) {
    return;
  }

  const activeRect = activeLink.getBoundingClientRect();
  const scrollerRect = scroller.getBoundingClientRect();
  const isVisible =
    activeRect.top >= scrollerRect.top &&
    activeRect.bottom <= scrollerRect.bottom;

  if (isVisible) {
    return;
  }

  const activeCenter = activeRect.top + activeRect.height / 2;
  const scrollerCenter = scrollerRect.top + scrollerRect.height / 2;

  scroller.scrollTo({
    top: scroller.scrollTop + activeCenter - scrollerCenter,
    behavior: "auto",
  });
}

function DocSidebarDesktop({ path, sidebar, onCollapse, isHidden }: Props) {
  const {
    docs: {
      sidebar: { hideable },
    },
  } = useThemeConfig();
  const location = useLocation();
  const sidebarRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (isHidden || !sidebarRef.current) {
      return undefined;
    }

    const animationFrame = window.requestAnimationFrame(() => {
      if (sidebarRef.current) {
        scrollActiveSidebarItemIntoView(sidebarRef.current);
      }
    });

    return () => window.cancelAnimationFrame(animationFrame);
  }, [isHidden, location.hash, location.pathname, location.search, path]);

  return (
    <div
      ref={sidebarRef}
      className={clsx(styles.sidebar, isHidden && styles.sidebarHidden)}
    >
      <div className={styles.sidebarHeader}>
        <div className={styles.sidebarBrand}>
          <Logo tabIndex={-1} className={styles.sidebarLogo} />
          <DocsVersionSelect />
        </div>
        <div className="docs-sidebar-search">
          <SearchBar />
        </div>
      </div>
      <Content path={path} sidebar={sidebar} />
      {hideable && <CollapseButton onClick={onCollapse} />}
    </div>
  );
}

export default React.memo(DocSidebarDesktop);
