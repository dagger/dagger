// The navbar / docs-sidebar search trigger button.
//
// Docusaurus renders @theme/SearchBar in two places (the navbar's `search`
// item and the swizzled docs sidebar), so there can be two of these buttons on
// a page. They share one open-state via ./store, and a single <SearchPalette>
// mounted at @theme/Root renders the actual command-palette overlay — so the
// palette opens once no matter which trigger (or Cmd/Ctrl+K) fires it.

import React from "react";
import useIsBrowser from "@docusaurus/useIsBrowser";

import {setOpen} from "./store";
import styles from "./styles.module.css";

export default function SearchBar(): JSX.Element {
  const isBrowser = useIsBrowser();
  const isMac = isBrowser && /Mac|iPhone|iPad/.test(navigator.platform || "");

  return (
    <button
      type="button"
      className={styles.trigger}
      aria-label="Search docs"
      onClick={() => setOpen(true)}
    >
      <svg width="15" height="15" viewBox="0 0 20 20" aria-hidden="true">
        <path
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          d="M8.5 3a5.5 5.5 0 1 0 3.5 9.74l4.13 4.13M8.5 3a5.5 5.5 0 0 1 4.3 8.93"
        />
      </svg>
      <span className={styles.triggerLabel}>Search</span>
      <span className={styles.kbd}>{isMac ? "⌘K" : "Ctrl K"}</span>
    </button>
  );
}
