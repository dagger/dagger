// THIS IS AN EXPERIMENTAL API AND IT COULD CHANGE AT ANY TIME IN A BREAKING
// MANNER BEFORE A MAJOR RELEASE.
//
// USE AT YOUR OWN RISK.

const { Microfiber: IntrospectionManipulator } = require('microfiber')

function sortByName(a, b) {
  if (a.name > b.name) {
    return 1
  }
  if (a.name < b.name) {
    return -1
  }

  return 0
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

  // This is a contrived example that shows how you can generate your own simple or nested set
  // of data and items to create the output you want for SpectaQL. This example will only
  // output the Queries in a sub-heading called "Queries" under an outer heading called "Operations",
  // and then all the "normal" Types in the schema under a heading called "Types".
  //
  // What should be noted is that you can nest things (more than once) for ultimate
  // control over what data you display, and in what arrangement. Yay!
  console.log(queryType.fields)
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