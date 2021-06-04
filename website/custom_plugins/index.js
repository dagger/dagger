module.exports = function (context, options) {
    return {
        name: "custom-docusaurus-plugin",
        configureWebpack(config, isServer, utils) {
            return {
                resolve: {
                    symlinks: false
                }
            };
        }
    };
};