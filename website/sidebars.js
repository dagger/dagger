/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.
 e.g. items: [{type: "autogenerated", dirName: "guides"}],

 Create as many sidebars as you want.
 */

 module.exports = {
  0.3: [
    {
      type: "doc",
      id: "get-started/bvtz9-get-started"
    },    
    {
      type: "category",
      label: "Learn",
      collapsible: false,
      collapsed: false,
      items: [
        {
          type: "category",
          label: "Concepts",
          collapsible: true,
          collapsed: true,
          items: [
            "concepts/zlci7-api",
            "concepts/hbf3z-extensions",
          ],
        },        
        {
          type: "category",
          label: "Tutorials",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "autogenerated",
              dirName: "tutorials"
            }
          ],
        },
        {
          type: "category",
          label: "Technical Guides",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "autogenerated",
              dirName: "guides"
            }            
          ],
        },
    
      ],
    },
    {
      type: "category",
      label: "Extensions",
      collapsible: false,
      collapsed: false,
      link: {
        type: "doc",
        id:  "extensions/h3lsa-index",
      },
      items: [
       
        {
          type: "category",
          label: "Languages",
          collapsible: true,
          collapsed: true,
          items: [
            "extensions/pknkx-rails",
          ],
        },
        {
          type: "category",
          label: "Deployment Targets",
          collapsible: true,
          collapsed: true,
          items: [
            "extensions/2f483-terraform",
            "extensions/saz1g-vercel",
          ],
        },
      ]
    },     

    {
      type: "category",
      label: "Reference",
      collapsible: false,
      collapsed: false,
      items: [
        {
          type: "category",
          label: "Dagger API",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "autogenerated",
              dirName: "reference/api"
            }
          ],
        },
      ]
    },     
    {
      type: "doc",
      label: "FAQ",
      id: "faq/fvt9k-faq",
    },


  ],
};
