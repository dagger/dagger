const fetch = require("node-fetch");

module.exports = function () {
    return {
        name: 'docusaurus-plugin-dagger-version',
        async loadContent() {
            var response = await fetch("https://dl.dagger.io/dagger/latest_version");
            var releases = await response.text();
            const version = releases || 'v0.2.18';

            return version;
        },
        async contentLoaded({content, actions}) {
            const {setGlobalData} = actions;
            setGlobalData({daggerVersionLatestRelease: content});
        },
    };
};
