import React, {useEffect, useState} from "react";
import Link from "@docusaurus/Link";
import styles from "@site/src/css/molecules/guideIndex.module.scss";
import guidesJSON from "@site/static/guides.json";
import Tag from "../atoms/tag";

export default function GuidesIndex() {
  const guides = guidesJSON.sort((a, b) => {
    return b.timestamp - a.timestamp;
  });

  const [filteredGuides, setFilteredGuides] = useState(guides);
  const [selectedTags, setSelectedTags] = useState([]);

  useEffect(() => {
    if (selectedTags.length > 0) {
      setFilteredGuides(
        guides.filter((item) =>
          selectedTags.every((tag) => item.frontMatter.tags.includes(tag)),
        ),
      );
    } else {
      setFilteredGuides(guides);
    }
  }, [selectedTags]);

  useEffect(() => {
    const tagURLParams = new URL(document.location).searchParams.getAll("tags");
    tagURLParams && setSelectedTags([...tagURLParams]);
  }, []);

  const pushTag = (tag) => {
    if (!selectedTags.some((x) => x === tag)) {
      if ("URLSearchParams" in window) {
        var searchParams = new URLSearchParams(window.location.search);
        searchParams.append("tags", tag);
        var newRelativePathQuery =
          window.location.pathname + "?" + searchParams.toString();
        history.pushState(null, "", newRelativePathQuery);
      }
      setSelectedTags([...selectedTags, tag]);
    }
  };

  const popTag = (tag) => {
    if ("URLSearchParams" in window) {
      var searchParams = new URLSearchParams(window.location.search);
      const path = window.location.pathname;
      let allSearchParams = searchParams.getAll("tags");
      if (allSearchParams.length === 1) {
        searchParams.delete("tags");
        let newRelativePathQuery = path;
        history.pushState(null, "", newRelativePathQuery);
      } else {
        searchParams.delete("tags")
        allSearchParams.forEach(x => x != tag ? searchParams.append("tags", x) : null)
        let newRelativePathQuery = path + "?" + searchParams.toString();
        history.pushState(null, "", newRelativePathQuery);
      }
    }
    setSelectedTags(selectedTags.filter((x) => x != tag));
  };

  return (
    <div className={styles.guideIndex}>
      <div className={styles.selectedTags}>
        {selectedTags.map((x, i) => (
          <Tag key={i} label={x} onCloseClick={() => popTag(x)} removable></Tag>
        ))}
      </div>
      <ul>
        {filteredGuides.map((x, i) => (
          <li key={i}>
            <GuideCard
              title={x.contentTitle}
              url={x.frontMatter.slug}
              tags={x.frontMatter.tags}
              authors={x.frontMatter.authors}
              timestamp={x.timestamp}
              pushTag={pushTag}
            />
          </li>
        ))}
      </ul>
    </div>
  );
}

function GuideCard({title, url, tags, authors, timestamp, pushTag}) {
  const handleAuthors = () => {
    let authorsString = "";
    authors.forEach((x) => (authorsString += `, ${x}`));
    return `By ${authorsString.slice(1)}`;
  };

  const dateOptions = {year: "numeric", month: "long", day: "numeric"};
  const date = new Date(timestamp).toLocaleDateString("en-US", dateOptions);

  return (
    <div className={styles.guideCard}>
      <div className={styles.info}>
        <Link href={url}>
          <h3>{title}</h3>
        </Link>
        {date && <time>{date}</time>}
        {authors && <h4 className={styles.author}>{handleAuthors()}</h4>}
      </div>
      <div className={styles.tags}>
        {tags &&
          tags.map((x, i) => (
            <Tag onTagClick={() => pushTag(x)} key={i} label={x} />
          ))}
      </div>
    </div>
  );
}
