const path = require("path");

module.exports = function (context) {
  const { siteConfig } = context;
  const { themeConfig } = siteConfig;
  const { hotjar } = themeConfig || {};

  if (!hotjar) {
    throw new Error(
      `Create a 'hotjar' object containing a 'siteId' property in 'themeConfig'.`
    );
  }

  const { siteId } = hotjar;

  if (!siteId) {
    throw new Error(
      "Error in `themeConfig`. `hotjar` object found but `siteId` prop is missing."
    );
  }

  return {
    name: "docusaurus-plugin-hotjar",

    injectHtmlTags() {
      return {
        headTags: [
          {
            tagName: "script",
            innerHTML: `
            <!-- Hotjar Tracking Code -->
            (function(h,o,t,j,a,r){
                h.hj=h.hj||function(){(h.hj.q=h.hj.q||[]).push(arguments)};
                h._hjSettings={hjid:${siteId},hjsv:6};
                a=o.getElementsByTagName('head')[0];
                r=o.createElement('script');r.async=1;
                r.src=t+h._hjSettings.hjid+j+h._hjSettings.hjsv;
                a.appendChild(r);
            })(window,document,'https://static.hotjar.com/c/hotjar-','.js?sv=');
            `,
          },
        ],
      };
    },
  };
};
