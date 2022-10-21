  export function queryBuilder(q: any) {

    const args = (item: any) => { 
      let regex = /\{"[a-zA-Z]+"/ig;
      
      return Object.entries(item.args)
        .map(value => {
            if(typeof value[1] === 'object') {
              return `${value[0]}: ${JSON.stringify(value[1]).replace(regex, str => str.replace(/"/g, ''))}`
            }
            if(typeof value[1] === 'number') {
              return `${value[0]}: ${value[1]}`
            }
          return `${value[0]}: "${value[1]}"`
        })
    }

    let query = ""
    q.forEach((item: any, index: any) => {
      query += `
        ${item.operation} ${item.args ? `(${args(item)})` : ''} ${q.length - 1 !== index ? '{' : '}'.repeat(q.length - 1)}  
      `
    })

    return query.replace(/\s+/g, '')
  }

  export function queryFlatten(res: any) {
    return Object.assign(
      {}, 
      ...function _flatten(o): any { 
        return [].concat(...Object.keys(o)
          .map((k: string) => {
            if(typeof o[k] === 'object' && !(o[k] instanceof Array)) return _flatten(o[k])
            if(o[k] instanceof Array) return {[k]: o[k]}
            else return ({[k]: o[k]})
            }
          )
        );
      }(res)
    )
  }