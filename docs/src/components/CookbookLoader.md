# CookbookLoader Component

A React TSX component for Docusaurus that loads and displays .mdx files filtered by cookbook tags.

## Features

1. **Directory scanning**: Loads .mdx files from a specified directory
2. **Tag filtering**: Filters files by `cookbook_tag` frontmatter field  
3. **Static generation**: Uses a Docusaurus plugin to generate cookbook data at build time
4. **Responsive rendering**: Displays title, description, and first ## heading from each file

## Setup

### 1. Install the Plugin

Add the cookbook plugin to your `docusaurus.config.ts`:

```typescript
plugins: [
  [
    './plugins/docusaurus-plugin-cookbook',
    {
      cookbookPath: './current_docs/partials/cookbook',
    },
  ],
  // ... other plugins
],
```

### 2. Use the Component

```tsx
import CookbookLoader from '@site/src/components/CookbookLoader';

// Display all cookbook files with the "filesystem" tag
<CookbookLoader cookbookTag="filesystem" />

// With custom styling
<CookbookLoader cookbookTag="filesystem" className="my-custom-class" />
```

## Expected File Structure

Cookbook files should have frontmatter like this:

```mdx
---
title: "Clone remote Git"
description: "Learn how to clone a remote Git repository into a container using Dagger."
cookbook_tag: filesystem
---

## Clone a remote Git repository into a container

Content goes here...
```

## Generated Data

The plugin generates a `static/cookbook.json` file with this structure:

```json
{
  "cookbookFiles": {
    "filesystem": [
      {
        "path": "/path/to/file.mdx",
        "frontMatter": {
          "title": "Clone remote Git",
          "description": "Learn how to clone...",
          "cookbook_tag": "filesystem"
        },
        "contentTitle": "Clone remote Git",
        "excerpt": "...",
        "firstHeading": "Clone a remote Git repository into a container"
      }
    ]
  },
  "tags": ["filesystem", "containers", ...]
}
```

## Props

- `cookbookTag: string` - The tag to filter cookbook files by
- `className?: string` - Optional CSS class name for the container

## Styling

The component uses CSS modules for styling. You can customize the appearance by:

1. Modifying `src/css/cookbookLoader.module.scss`
2. Adding your own CSS classes via the `className` prop
3. Using CSS custom properties (CSS variables) for theming
