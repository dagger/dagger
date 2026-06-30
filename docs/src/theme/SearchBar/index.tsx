// Pure client-side docs search, modeled on github.com/vito/dang.
//
// A command-palette overlay (Cmd/Ctrl+K, or click the navbar button) searches
// a small JSON index — generated at build time by plugins/local-search — fully
// in the browser. The index is fetched lazily on first open, so it costs
// nothing on initial page load, and every keystroke is an in-memory lookup.

import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {createPortal} from "react-dom";
import {useHistory, useLocation} from "@docusaurus/router";
import useBaseUrl from "@docusaurus/useBaseUrl";
import useIsBrowser from "@docusaurus/useIsBrowser";

import styles from "./styles.module.css";

type Entry = {
  version: string; // "" for the default version, "next" for the unreleased docs
  title: string;
  crumb: string;
  location: string;
  text: string;
  haystack: string;
};

type Scored = {entry: Entry; score: number; terms: string[]};

const MAX_RESULTS = 30;

// Fetch-once cache, shared across mounts so reopening is instant.
let cachedEntries: Entry[] | null = null;
let inflight: Promise<Entry[]> | null = null;

function loadIndex(url: string): Promise<Entry[]> {
  if (cachedEntries) return Promise.resolve(cachedEntries);
  if (inflight) return inflight;
  inflight = fetch(url)
    .then((r) => {
      if (!r.ok) throw new Error("failed to load search index");
      return r.json();
    })
    .then((data: Omit<Entry, "haystack">[]) => {
      cachedEntries = data.map((e) => ({
        ...e,
        haystack: (`${e.title || ""} ${e.text || ""}`).toLowerCase(),
      }));
      return cachedEntries;
    })
    .catch((err) => {
      inflight = null; // allow retry on next open
      throw err;
    });
  return inflight;
}

function search(query: string, entries: Entry[]): Scored[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!terms.length) return [];
  const scored: Scored[] = [];
  for (const entry of entries) {
    let ok = true;
    let score = 0;
    const title = entry.title.toLowerCase();
    for (const term of terms) {
      if (entry.haystack.indexOf(term) === -1) {
        ok = false;
        break;
      }
      const ti = title.indexOf(term);
      if (ti === 0) score += 12;
      else if (ti > 0) score += 6;
      else score += 1;
    }
    if (ok) scored.push({entry, score, terms});
  }
  scored.sort(
    (a, b) => b.score - a.score || a.entry.title.length - b.entry.title.length,
  );
  return scored.slice(0, MAX_RESULTS);
}

function escapeHtml(s: string): string {
  return s.replace(
    /[&<>"]/g,
    (c) => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;"})[c]!,
  );
}

// Wrap every term occurrence in an already-escaped string with <mark>.
function highlight(escaped: string, terms: string[]): string {
  let out = escaped;
  for (const term of terms) {
    const re = new RegExp(
      `(${term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")})`,
      "ig",
    );
    out = out.replace(re, "\u0000$1\u0001");
  }
  return out.split("\u0000").join("<mark>").split("\u0001").join("</mark>");
}

// A snippet centered on the first matching term.
function snippet(text: string, terms: string[]): string {
  const lower = text.toLowerCase();
  let at = -1;
  for (const term of terms) {
    const idx = lower.indexOf(term);
    if (idx !== -1 && (at === -1 || idx < at)) at = idx;
  }
  if (at === -1) return text.slice(0, 140);
  const start = Math.max(0, at - 50);
  const end = Math.min(text.length, at + 110);
  return (
    (start > 0 ? "…" : "") +
    text.slice(start, end) +
    (end < text.length ? "…" : "")
  );
}

export default function SearchBar(): JSX.Element {
  const history = useHistory();
  const indexUrl = useBaseUrl("/search_index.json");
  const isBrowser = useIsBrowser();

  // Scope results to the version the reader is currently on: "next" under the
  // unreleased docs, otherwise the default version served at the root.
  const {pathname} = useLocation();
  const nextBase = useBaseUrl("/next/");
  const vkey =
    pathname === nextBase.slice(0, -1) || pathname.startsWith(nextBase)
      ? "next"
      : "";

  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [entries, setEntries] = useState<Entry[] | null>(cachedEntries);
  const [error, setError] = useState(false);
  const [active, setActive] = useState(0);

  const inputRef = useRef<HTMLInputElement>(null);
  const resultsRef = useRef<HTMLUListElement>(null);

  const isMac =
    isBrowser && /Mac|iPhone|iPad/.test(navigator.platform || "");

  const results = useMemo(
    () =>
      entries && query
        ? search(
            query,
            entries.filter((e) => (e.version || "") === vkey),
          )
        : [],
    [entries, query, vkey],
  );

  // Load index + focus input whenever the palette opens.
  useEffect(() => {
    if (!open) return;
    setError(false);
    loadIndex(indexUrl).then(setEntries).catch(() => setError(true));
    const id = window.requestAnimationFrame(() => inputRef.current?.focus());
    return () => window.cancelAnimationFrame(id);
  }, [open, indexUrl]);

  // Reset the highlighted row when the result set changes.
  useEffect(() => setActive(0), [results]);

  // Global Cmd/Ctrl+K to toggle, Escape to close.
  useEffect(() => {
    function onKey(ev: KeyboardEvent) {
      if ((ev.ctrlKey || ev.metaKey) && (ev.key === "k" || ev.key === "K")) {
        ev.preventDefault();
        setOpen((o) => !o);
      } else if (ev.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const move = useCallback(
    (delta: number) => {
      setActive((a) => {
        if (!results.length) return 0;
        const next = (a + delta + results.length) % results.length;
        const el = resultsRef.current?.children[next] as HTMLElement | undefined;
        el?.scrollIntoView({block: "nearest"});
        return next;
      });
    },
    [results.length],
  );

  const go = useCallback(
    (idx: number) => {
      const hit = results[idx];
      if (!hit) return;
      setOpen(false);
      history.push(hit.entry.location);
    },
    [results, history],
  );

  const onInputKeyDown = useCallback(
    (ev: React.KeyboardEvent) => {
      if (ev.key === "ArrowDown") {
        ev.preventDefault();
        move(1);
      } else if (ev.key === "ArrowUp") {
        ev.preventDefault();
        move(-1);
      } else if (ev.key === "Enter") {
        ev.preventDefault();
        go(active);
      } else if (ev.key === "Escape") {
        ev.preventDefault();
        setOpen(false);
      }
    },
    [move, go, active],
  );

  const trigger = (
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

  const overlay =
    open && isBrowser
      ? createPortal(
          <div
            className={styles.overlay}
            onMouseDown={(ev) => {
              if (ev.target === ev.currentTarget) setOpen(false);
            }}
          >
            <div
              className={styles.box}
              role="dialog"
              aria-modal="true"
              aria-label="Search docs"
            >
              <input
                ref={inputRef}
                className={styles.input}
                type="text"
                placeholder="Search the docs…"
                autoComplete="off"
                spellCheck={false}
                aria-label="Search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={onInputKeyDown}
              />
              <ul className={styles.results} ref={resultsRef}>
                {error ? (
                  <li className={styles.message}>
                    Couldn’t load the search index.
                  </li>
                ) : !query ? null : !results.length ? (
                  <li className={styles.message}>
                    No results for “{query}”
                  </li>
                ) : (
                  results.map((r, i) => (
                    <li
                      key={r.entry.location}
                      className={i === active ? styles.active : undefined}
                    >
                      <a
                        href={r.entry.location}
                        onMouseEnter={() => setActive(i)}
                        onClick={(ev) => {
                          ev.preventDefault();
                          go(i);
                        }}
                      >
                        {r.entry.crumb ? (
                          <span className={styles.crumb}>{r.entry.crumb}</span>
                        ) : null}
                        <span
                          className={styles.title}
                          // eslint-disable-next-line react/no-danger
                          dangerouslySetInnerHTML={{
                            __html: highlight(
                              escapeHtml(r.entry.title),
                              r.terms,
                            ),
                          }}
                        />
                        {r.entry.text ? (
                          <span
                            className={styles.snippet}
                            // eslint-disable-next-line react/no-danger
                            dangerouslySetInnerHTML={{
                              __html: highlight(
                                escapeHtml(snippet(r.entry.text, r.terms)),
                                r.terms,
                              ),
                            }}
                          />
                        ) : null}
                      </a>
                    </li>
                  ))
                )}
              </ul>
            </div>
          </div>,
          document.body,
        )
      : null;

  return (
    <>
      {trigger}
      {overlay}
    </>
  );
}
