import React from "react";
import { useHistory } from "@docusaurus/router";
import {
  useActiveDocContext,
  useLatestVersion,
  useVersions,
} from "@docusaurus/plugin-content-docs/client";
import type { GlobalVersion } from "@docusaurus/plugin-content-docs/client";

type Props = {
  className?: string;
};

// The docs hooks take a plugin id positionally; undefined means "the site's
// only docs plugin", which is what the built-in version dropdown passes too.
const DOCS_PLUGIN_ID = undefined;

// A version picker has to answer two questions: which version am I reading, and
// where does each of the others live. Both answers belong to Docusaurus, which
// owns the version-to-URL mapping via the `versions` config. Deriving them
// instead from versions.json plus a hand-maintained "latest" constant means the
// picker silently lies whenever the two drift apart.
export default function DocsVersionSelect({ className }: Props) {
  const history = useHistory();
  const versions = useVersions(DOCS_PLUGIN_ID);
  const latestVersion = useLatestVersion(DOCS_PLUGIN_ID);
  const { activeVersion, alternateDocVersions } =
    useActiveDocContext(DOCS_PLUGIN_ID);
  // activeVersion is undefined off the docs routes (404, /restricted).
  const selected = activeVersion ?? latestVersion;

  // A version's root path is not always a page: the unreleased docs at /next
  // have no doc at slug "/", so /next/ is a bare directory (404 in the app,
  // 403 from the nginx preview). Resolve to a doc instead, the same way the
  // built-in docsVersionDropdown does: the page being read if it exists in
  // the target version, otherwise that version's main doc.
  function targetPath(version: GlobalVersion): string {
    const doc =
      alternateDocVersions[version.name] ??
      version.docs.find((d) => d.id === version.mainDocId);
    return doc?.path ?? version.path;
  }

  // Released versions first, unreleased last, matching how readers scan the
  // list. useVersions() orders newest-first, which would put "Next" on top.
  const ordered = [
    ...versions.filter((version) => version.name !== "current"),
    ...versions.filter((version) => version.name === "current"),
  ];

  return (
    <select
      aria-label="Docs version"
      className={className}
      value={selected.name}
      onChange={(event) => {
        const target = versions.find(
          (version) => version.name === event.currentTarget.value,
        );
        if (target) {
          history.push(targetPath(target));
        }
      }}
    >
      {ordered.map((version) => (
        <option key={version.name} value={version.name}>
          {version.label}
        </option>
      ))}
    </select>
  );
}
