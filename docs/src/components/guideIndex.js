import React, {useEffect, useState} from "react";
import Link from "@docusaurus/Link";
import styles from "@site/src/css/guideIndex.module.scss";
import guidesJSON from "@site/static/guides.json";
import Tag from "./tag";

export default function GuidesIndex() {
  const guides = guidesJSON.guides;
  const allTags = guidesJSON.allTags;
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
        searchParams.delete("tags");
        allSearchParams.forEach((x) =>
          x != tag ? searchParams.append("tags", x) : null,
        );
        let newRelativePathQuery = path + "?" + searchParams.toString();
        history.pushState(null, "", newRelativePathQuery);
      }
    }
    setSelectedTags(selectedTags.filter((x) => x != tag));
  };

  return (
    <div className={styles.guideIndex}>
      <GuideFilter
        allTags={allTags}
        selectedTags={selectedTags}
        onCloseClick={popTag}
        pushTag={pushTag}
      />
      <ul>
        {filteredGuides.map((x, i) => (
          <li key={i}>
            <GuideCard
              title={x.contentTitle}
              url={x.frontMatter.slug}
              tags={x.frontMatter.tags}
              authors={x.frontMatter.authors}
              pushTag={pushTag}
            />
          </li>
        ))}
      </ul>
    </div>
  );
}

function GuideCard({title, url, tags, authors, pushTag}) {
  const handleAuthors = () => {
    let authorsString = "";
    authors.forEach((x) => (authorsString += `, ${x}`));
    return `By ${authorsString.slice(1)}`;
  };

  return (
    <div className={styles.guideCard}>
      <div className={styles.info}>
        <Link href={url}>
          <h3>{title}</h3>
        </Link>
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

function GuideFilter({allTags, selectedTags, onCloseClick, pushTag}) {
  const [filtering, setFiltering] = useState(false);
  const [filteredTags, setFilteredTags] = useState(allTags);
  const [query, setQuery] = useState("");

  const handleChange = (e) => {
    let newQuery = e.target.value.toLowerCase();
    const results = allTags.filter((tag) => {
      if (newQuery === "") return allTags;
      return tag.toLowerCase().includes(e.target.value.toLowerCase());
    });
    setQuery(newQuery);
    setFilteredTags(results);
  };

  const handlePushTag = (x) => {
    pushTag(x)
    setQuery("");
    setFilteredTags(allTags);
  };

  return (
    <div
      className={styles.filterWrapper}
      onMouseLeave={() => setFiltering(false)}>
      <div className={styles.filter}>
        {(selectedTags.length > 0 && <div className={styles.filterTags}>
          {selectedTags.map((x, i) => (
            <Tag
              key={i}
              label={x}
              removable
              onCloseClick={() => onCloseClick(x)}
            />
          ))}
        </div>)}
        <input
          onClick={() => setFiltering(true)}
          className={`${styles.filterInput} ${selectedTags.length > 0 ? styles.filterInputHasTags : ''}`}
          placeholder="Filter guides by tag"
          type="search"
          value={query}
          onChange={handleChange}></input>
      </div>
      {filtering && (
        <div className={styles.filterDropdown}>
          <ul>
            {filteredTags.map((x, i) => (
              <li
                key={i}
                onClick={() => {
                  handlePushTag(x);
                }}>
                {x}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
