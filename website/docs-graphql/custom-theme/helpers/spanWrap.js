// Wraps a string in a span with a class
//
// Options:
//   clazz (String)
module.exports = function (value, options) {
    if (options?.hash?.isField) {
        return `<a class="${options?.hash?.className || ''}" id="${options?.hash?.id || ""}" href="#${options?.hash?.id}">${value}</a>`
    } else {
        return `<span class="${options?.hash?.className || ''}">${value}</span>` 
    }
  }