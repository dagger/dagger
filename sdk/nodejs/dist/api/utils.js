export function queryBuilder(q) {
    const args = (item) => {
        let regex = /\{"[a-zA-Z]+"/ig;
        return Object.entries(item.args)
            .map(value => {
            if (typeof value[1] === 'object') {
                return `${value[0]}: ${JSON.stringify(value[1]).replace(regex, str => str.replace(/"/g, ''))}`;
            }
            if (typeof value[1] === 'number') {
                return `${value[0]}: ${value[1]}`;
            }
            return `${value[0]}: "${value[1]}"`;
        });
    };
    let query = "{";
    q.forEach((item, index) => {
        query += `
        ${item.operation} ${item.args ? `(${args(item)})` : ''} ${q.length - 1 !== index ? '{' : '}'.repeat(q.length - 1)}
      `;
    });
    query += "}";
    return query.replace(/\s+/g, '');
}
export function queryFlatten(res) {
    if (!res) {
        console.log("🐞 --------------------------------------------------🐞");
        console.log("🐞 ~ Graphql Error response");
        console.log("🐞 --------------------------------------------------🐞");
    }
    return Object.assign({}, ...function _flatten(o) {
        return [].concat(...Object.keys(o)
            .map((k) => {
            if (typeof o[k] === 'object' && !(o[k] instanceof Array))
                return _flatten(o[k]);
            else
                return { [k]: o[k] };
        }));
    }(res));
}
