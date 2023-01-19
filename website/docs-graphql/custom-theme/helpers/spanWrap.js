// Wraps a string in a span with a class
//
// Options:
//   clazz (String)
module.exports = function (value, options) {
    if (options?.hash?.isField) {
        let id = `${options?.hash?.prefix}-${options?.hash?.id || ""}`
        return `<a class="${options?.hash?.className || ''}" id="${id}" href="#${id}">${value}</a>`
    } else {
        return `<span class="${options?.hash?.className || ''}">${value}</span>` 
    }
  }