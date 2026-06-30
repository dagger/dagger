// Local, client-side docs search — no external service.
//
// At build time this walks the rendered HTML of the *current* (default) docs
// version and emits a flat `search_index.json` of section-level entries. The
// search UI (src/theme/SearchBar) fetches that index lazily on first open and
// does all matching in the browser, so every keystroke is an in-memory lookup
// with zero network round-trips.
//
// Modeled on the pure-client-side search in github.com/vito/dang.

const fs = require("fs");
const path = require("path");
const cheerio = require("cheerio");

// Cap per-section body text so the index stays small and fast to ship. This is
// plenty for matching and for building a centered snippet.
const MAX_TEXT = 2000;

// Title-case a URL path segment: "type-system" -> "Type System".
function humanize(segment) {
  return segment
    .split("-")
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

// Build a breadcrumb-ish label from a route's parent path, e.g.
// "/extending/type-system/" -> "Extending › Type System".
function crumbFromRoute(rel) {
  const parts = rel.replace(/^\/|\/$/g, "").split("/").filter(Boolean);
  parts.pop(); // drop the page's own segment
  return parts.map(humanize).join(" › ");
}

function collapse(text) {
  return (text || "").replace(/\s+/g, " ").trim();
}

module.exports = function localSearchPlugin(context, options) {
  const indexFileName = (options && options.indexFileName) || "search_index.json";
  const baseUrl = context.baseUrl || "/";
  // Routes whose path contains any of these are kept out of the index. The
  // auto-generated per-SDK reference is excluded so it doesn't dominate search.
  const exclude = (options && options.exclude) || ["/reference/typescript/"];

  return {
    name: "local-search",

    async postBuild({ outDir, routesPaths }) {
      const nextPrefix = baseUrl + "next/";
      const entries = [];

      for (const route of routesPaths) {
        if (!route.startsWith(baseUrl)) continue;
        // Only index the current/default version (served at the root). The
        // unreleased "next" docs and old versions are intentionally excluded.
        if (route.startsWith(nextPrefix)) continue;
        if (exclude.some((frag) => route.includes(frag))) continue;

        const rel = "/" + route.slice(baseUrl.length); // normalize to "/foo/"
        const htmlFile = path.join(outDir, route.slice(baseUrl.length), "index.html");
        if (!fs.existsSync(htmlFile)) continue;

        const $ = cheerio.load(fs.readFileSync(htmlFile, "utf8"));
        const article = $("article");
        const root = article.find(".theme-doc-markdown").first();
        // Skip non-doc pages (no rendered markdown container).
        if (!root.length) continue;

        const pageTitle = collapse(
          root.find("h1").first().text() || $("title").first().text().split("|")[0],
        );
        if (!pageTitle) continue;

        // Split the page into sections at each h2/h3 so results land on the
        // right anchor. Text before the first heading becomes the page entry.
        const sections = [];
        let cur = { id: null, title: pageTitle, parts: [] };
        root.children().each((_, el) => {
          const tag = (el.tagName || "").toLowerCase();
          if (tag === "h2" || tag === "h3") {
            sections.push(cur);
            const h = $(el);
            const id = h.attr("id") || null;
            const title = collapse(h.clone().find(".hash-link").remove().end().text());
            cur = { id, title: title || pageTitle, parts: [] };
          } else {
            const txt = $(el).text();
            if (txt) cur.parts.push(txt);
          }
        });
        sections.push(cur);

        const pageCrumb = crumbFromRoute(rel);
        for (const s of sections) {
          const text = collapse(s.parts.join(" ")).slice(0, MAX_TEXT);
          if (!s.id) {
            // Page-level entry; skip an empty intro when the page has sections.
            if (!text && sections.length > 1) continue;
            entries.push({
              title: pageTitle,
              crumb: pageCrumb,
              location: route,
              text,
            });
          } else {
            if (!text && !s.title) continue;
            entries.push({
              title: s.title,
              crumb: pageTitle,
              location: route + "#" + s.id,
              text,
            });
          }
        }
      }

      fs.writeFileSync(
        path.join(outDir, indexFileName),
        JSON.stringify(entries),
      );
      console.log(`[local-search] indexed ${entries.length} sections -> ${indexFileName}`);
    },
  };
};
