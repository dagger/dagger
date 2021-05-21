import React, { useEffect, useState } from "react";
import { Select as SelectStyled } from '../styles';

const Select = () => {
    const isBrowser = typeof window !== "undefined"
    const [tagsList, setTagsList] = useState([])
    const currentLocation = isBrowser ? window.location.pathname.split('/') : []
    currentLocation.pop() //remove last trailing slash

    useEffect(() => {
        async function getTags() {
            const fetchTags = await fetch('https://s3-eu-west-1.amazonaws.com/daggerdoc.humans-it.com/tags.json').then(result => result.json());
            setTagsList(fetchTags);
        }

        getTags()
    }, [])

    return (
        <SelectStyled value={currentLocation[currentLocation.length - 1]} onChange={(e) => isBrowser ? window.location.pathname = `/${e.currentTarget.value}/index.html` : null}>
            {tagsList.map(t => (
                <option>{t.tag}</option>
            ))}
        </SelectStyled>
    );
};

export default Select;
