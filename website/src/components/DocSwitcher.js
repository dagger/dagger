import React, {useState} from 'react';
import Link from '@docusaurus/Link';
import {useHistory} from '@docusaurus/router';

function DocSwitcher() {
  const [toggleDocs, setToggleDocs] = useState({path: '/', label: 'Europa'});
  const history = useHistory();
  const userStorage =
    typeof window !== 'undefined'
      ? JSON.parse(localStorage.getItem('user'))
      : null;
  const userAllowed = process.env.REACT_APP_ALLOWED_USER.split(',').includes(
    userStorage?.login,
  );

  function toggleLink(event) {
    event.preventDefault();
    if (toggleDocs.path === '/') {
      setToggleDocs(
        {path: '/1200/local-ci', label: 'Alpha'},
        history.push('/1200/local-ci'),
      );
    } else {
      setToggleDocs({path: '/', label: 'Europa'}, history.push('/'));
    }
  }

  return userStorage?.login && userAllowed ? (
    <Link
      to={toggleDocs.path}
      style={{color: 'white'}}
      onClick={(event) => toggleLink(event)}>
      Switch to {toggleDocs.label}
    </Link>
  ) : null;
}

export default DocSwitcher;
