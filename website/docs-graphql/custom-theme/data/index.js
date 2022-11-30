/**
 * This is the data source entrypoint for handlebars.
 * 
 * From the speactaql docs, this API is experimental, so
 * it may break with future releases.
 * 
 * This script takes the output from Microfiber, a tool
 * that parses introspection schemas or .gql files and 
 * outputs the results in a friendly manner.
 * 
 */

const { Microfiber: IntrospectionManipulator } = require('microfiber')
const fs = require('fs');
const path = require('path')
const chalk = require('chalk')

const examplesPath = path.resolve(`${__dirname}/../../data/examples`);

console.log("Test")

function sortByName(a, b) {
  if (a.name > b.name) {
    return 1
  }
  if (a.name < b.name) {
    return -1
  }

  return 0
}

function getExamplesByName(name) {
  try {
    console.log(chalk.blue(`Found example to be rendered for ${name}`))
    let content = fs.readFileSync(path.resolve(`${examplesPath}/queries/${name}/gql.md`), 'utf8')
    console.log(chalk.blue(`Rendering successful.`))
    return [content]
  } catch(err) {
    console.log(chalk.red(`Error processing the example for ${name}.`), chalk.red(err))
  }
}

module.exports = ({
  // The Introspection Query Response after all the augmentation and metadata directives
  // have been applied to it
  introspectionResponse,
  // All the options that are specifically for the introspection related behaviors, such a this area.
  introspectionOptions,
  // A GraphQLSchema instance that was constructed from the provided introspectionResponse
  graphQLSchema: _graphQLSchema,
  // All of the SpectaQL options in case you need them for something.
  allOptions: _allOptions,
}) => {
  const introspectionManipulator = new IntrospectionManipulator(
    introspectionResponse,
    // microfiberOptions come from converting some of the introspection options to a corresponding
    // option for microfiber via the `src/index.js:introspectionOptionsToMicrofiberOptions()` function
    introspectionOptions?.microfiberOptions,
  )

  const queryType = introspectionManipulator.getQueryType()
  const normalTypes = introspectionManipulator.getAllTypes({
    includeQuery: false,
    includeMutation: false,
    includeSubscription: false,
  })

  return [
    // The idea is to return an Array of Objects with the following structure:
    {
      // The name for this group of items wherever it will be displayed
      name: 'Queries',
      makeContentSection: true,
      items: queryType.fields
        .map((query) => ({
          ...query,
          // What is this thing? Pick one:
          //
          // Is this item a Query?
          isQuery: true,
          // Is this item a Mutation?
          isMutation: false,
          // Is this item a Subscription?
          isSubscription: false,
          // Is this item a Type?
          isType: false,
          // Get all examples for the query and return as array
          examples: getExamplesByName(query.name)
        }))
        .sort(sortByName),
    },
    {
      name: 'Types',
      makeContentSection: true,
      items: normalTypes
        .map((type) => ({
          ...type,
          isType: true,
        }))
        .sort(sortByName),
    },
  ].filter(Boolean)
}