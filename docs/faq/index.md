---
slug: /faq
displayed_sidebar: europa
---

# FAQ

```mdx-code-block
import DocCardList from '@theme/DocCardList';
import {useDocsVersion} from '@docusaurus/theme-common';

export const FaqItems = () => {
    {/* access root category object from Docusaurus */}
    const docsVersion = useDocsVersion();
    {/* customProps object retrieved from sidebar.js */}
    const faqItem = docsVersion.docsSidebars.europa.filter(item => item.label === 'FAQ');
    {/* Return custom FAQ Items array */}
    const customPropsItem = faqItem[0].customProps.items.map(customPropsItem => {
        const result = Object.values(docsVersion.docs).filter(item => item.id === customPropsItem.docId)[0]
        if(result)
            return {
                type: "link",
                label: result.title,
                description: result.description,
                href: customPropsItem.href,
                docId: customPropsItem.docId
            }
    })
    return <DocCardList items={customPropsItem}/>
}

<FaqItems />
```
