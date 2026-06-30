// Swizzled @theme/Root wraps the whole app and mounts exactly once, so it's
// where the single search-palette overlay lives. The SearchBar triggers (navbar
// + docs sidebar) only open the shared store; this is the one component that
// renders the command palette, which is what keeps it from opening twice.

import React, {type ReactNode} from "react";

import SearchPalette from "./SearchBar/Palette";

export default function Root({children}: {children: ReactNode}): ReactNode {
  return (
    <>
      {children}
      <SearchPalette />
    </>
  );
}
