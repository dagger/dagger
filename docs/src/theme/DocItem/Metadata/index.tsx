import React, {type ReactNode} from "react";
import {PageMetadata} from "@docusaurus/theme-common";
import {useDoc, useDocsVersion} from "@docusaurus/plugin-content-docs/client";

export default function DocItemMetadata(): ReactNode {
  const {metadata, frontMatter, assets} = useDoc();
  const version = useDocsVersion();
  const archived = !version.isLast && version.version !== "current";
  const prefix = `Dagger ${version.label} (old version): `;

  return (
    <PageMetadata
      title={archived ? prefix + metadata.title : metadata.title}
      description={archived ? prefix + metadata.description : metadata.description}
      keywords={frontMatter.keywords}
      image={assets.image ?? frontMatter.image}
    />
  );
}
