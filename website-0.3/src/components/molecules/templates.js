import React from "react";
import CodeBlock from "@theme/CodeBlock";
import styles from "../../css/install.module.scss";

const Templates = ({templates}) => {
  return (
    <div className={styles.templates}>
      {templates.map((x, index) => (
        <TemplateCard
          key={index}
          label={x.label}
          imgFilename={x.imgFilename}
          url={x.url}
          dir={x.dir}
          cmd={x.cmd}
        />
      ))}
    </div>
  );
};

const TemplateCard = ({label, imgFilename, url, dir, cmd}) => {
  return (
    <div className={styles.templateCard}>
      <div className={styles.templateInfo}>
        <img src={`/img/getting-started/install-dagger/${imgFilename}`}></img>
        <a href={url}>{label}</a>
      </div>
      <CodeBlock>
        git clone {url}
        {`\n`}
        cd {dir}
        {`\n`}
        {cmd}
      </CodeBlock>
    </div>
  );
};

export default Templates;
