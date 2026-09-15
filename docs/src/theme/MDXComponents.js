import React from 'react';
// Import the original mapper
import MDXComponents from '@theme-original/MDXComponents';
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';
import CodeBlock from '@theme/CodeBlock';
import { daggerVersion } from '@site/current_docs/partials/version';


export default {
    // Re-use the default mapping
    ...MDXComponents,
    // register components to the global scope so we don't have to import them in every file
    // docs: https://docusaurus.io/docs/markdown-features/react#mdx-component-scope
    Tabs,
    TabItem,
    CodeBlock,
    daggerVersion: daggerVersion,
};