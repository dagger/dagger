import React from "react";
import ComponentTypes from "@theme-original/NavbarItem/ComponentTypes";

import DocsVersionSelect from "../../components/DocsVersionSelect";

// The mobile version picker used to be a `type: "html"` navbar item. Static
// markup can't mark an option as selected, so it always displayed the first
// version in the list regardless of which one the reader was actually on.
function DocsVersionSelectNavbarItem({ className }: { className?: string }) {
  return (
    <div className={className}>
      <DocsVersionSelect className="docs-version-select" />
    </div>
  );
}

export default {
  ...ComponentTypes,
  "custom-docsVersionSelect": DocsVersionSelectNavbarItem,
};
