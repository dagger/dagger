const fetch = require("node-fetch");

module.exports = function () {
    return {
        name: 'docusaurus-plugin-dagger-version',
        async loadContent() {
            var response = await fetch("https://api.github.com/repos/dagger/dagger/releases?per_page=1");
            var releases = await response.json();
            const version = releases[0] ? releases[0].tag_name : 'v0.2.11';
	    return version;
        },
        async contentLoaded({content, actions}) {
            const {setGlobalData} = actions;
            setGlobalData({daggerVersionLatestRelease: content});
        },
    };
};
