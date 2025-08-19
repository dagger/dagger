declare module "@site/static/cookbook.json" {
  import { CookbookFile } from "@site/plugins/docusaurus-plugin-cookbook";
  
  interface CookbookData {
    cookbookFiles: { [tag: string]: CookbookFile[] };
    tags: string[];
  }
  
  const cookbook: CookbookData;
  export default cookbook;
}
