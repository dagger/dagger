/* eslint-disable @typescript-eslint/no-explicit-any */
import { QueryTree } from "./client.gen.js";

  export function queryBuilder(q: QueryTree[]) {

    const args = (item: any) => { 
      const regex = /\{"[a-zA-Z]+"/ig;
      
      const entries = Object.entries(item.args)
        .filter(value => value[1] !== undefined)
        .map(value => {
            if(typeof value[1] === 'object') {
              return `${value[0]}: ${JSON.stringify(value[1]).replace(regex, str => str.replace(/"/g, ''))}`
            }
            if(typeof value[1] === 'number') {
              return `${value[0]}: ${value[1]}`
            }
          return `${value[0]}: "${value[1]}"`
        })
      if (entries.length === 0) {
              return ''
      }
      return '('+entries+')'
    }


    let query = "{"
    q.forEach((item: QueryTree, index: number) => {
      query += `
        ${item.operation} ${item.args ? `${args(item)}` : '' } ${q.length - 1 !== index ? '{' : '}'.repeat(q.length - 1)}
      `
    })
    query += "}"

    return query.replace(/\s+/g, '')
  }

  export function queryFlatten(res: Record<string, any>) : Record<string, any> {
    if (res.errors) throw res.errors[0]
    return Object.assign(
      {}, 
      ...function _flatten(o): any { 
        return [].concat(...Object.keys(o)
          .map((k: string) => {
            if(typeof o[k] === 'object' && !(o[k] instanceof Array)) return _flatten(o[k])
            else return {[k]: o[k]}
            }
          )
        );
      }(res)
    )
  }
