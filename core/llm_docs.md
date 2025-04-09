### 📘 How the Tool-Calling System Works

You interact with a tool system that mirrors a GraphQL API.

Below is an example usage of the tool calling scheme. Note that all potato and
cooking-related concepts are placeholders. In practice there will be entirely
different types of objects and APIs.

NOTE: the following is just a mock-up to give you an idea of the overall tool calling scheme. Don't pay any attention to the particular syntax.

<example>
  <user>
    <tools>
      # Gain new tools for subsequent turns.
      #
      # Available objects:
      # - Oven#1: An oven you can use for cooking.
      # - Potato#1: The raw potato to turn into french fries.
      # - Sink#1: A sink for cleaning food.
      # - Plate#1: A plate for serving food.
      #
      # Available tools:
      # - Oven_bake
      # - Oven_broil
      # - Potato_dice
      # - Potato_peel
      # - Potato_slice
      # - Sink_rinse
      # - Fries_plate
      selectTools(
        tools: [string],
      )

      # Complete your task and return its outputs to the user.
      returnToUser(
        # File ID observed from a tool result, in "File#number" format.
        #
        # The baked french fries.
        fries: string,
      )
    </tools>
    <prompt>
      Bake me some french fries.
    </prompt>
  </user>
  <assistant>
    <toolCall id="initialToolsCall">
      selectTools(tools: ["Potato_rinse", "Potato_peel", "Potato_slice", "Oven_bake"])
    </toolCalls>
  </assistant>
  <user>
    <tools>
      # Gain new tools for subsequent turns.
      #
      # Available objects:
      # - Oven#1: An oven you can use for cooking.
      # - Potato#1: The raw potato to turn into french fries.
      # - Sink#1: A sink for cleaning food.
      #
      # Available tools:
      # - Oven_bake
      # - Oven_broil
      # - Potato_dice
      # - Potato_peel
      # - Potato_rinse
      # - Potato_slice
      selectTools(
        # The tools to select.
        tools: [string],
      )

      # Complete your task and return its outputs to the user.
      returnToUser(
        # Fries ID observed from a tool result, in "Fries#number" format.
        #
        # The baked french fries.
        fries: string,
      )

      # Retrieves a cleaned potato by washing it in a sink.
      Potato_rinse(
        # The Potato to operate against.
        Potato: string,
        # The sink to use to rinse the potato.
        sink: string
      )

      # Retrieves this potato without its skin.
      Potato_skin(
        # The Potato to operate against.
        Potato: string
      )

      # Retrieves this potato without its skin.
      Potato_slice(
        # The Potato to operate against.
        Potato: string,
        # The shape to cut the potato into.
        # @enum("STICKS", "SLICES")
        shape: string
      )

      # Bakes a food item.
      Oven_bake(
        # The Oven to operate against.
        Oven: string,

        # The item to insert into the oven.
        item: string
      )
    </tools>
    <toolResult id="initialToolsCall">
      ok
    </toolResult>
  </user>
  <assistant>
    <toolCall id="rinsePotato">
      Potato_rinse(Potato: "Potato#1", sink: "Sink#1")
    </toolCall>
  </assistant>
  <user>
    <tools>
      ... same as before ...
    </tools>
    <toolResult id="rinsePotato">
      {"result":"Potato#2"}
    </toolResult>
  </user>
  <assistant>
    <toolCall id="peelPotato">
      Potato_peel(Potato: "Potato#2")
    </toolCall>
  </assistant>
  <user>
    <tools>
      ... same as before ...
    </tools>
    <toolResult id="slicePotato">
      {"result":"Potato#3"}
    </toolResult>
  </user>
  <assistant>
    <toolCall id="slicePotato">
      Potato_slice(Potato: "Potato#3", shape: "STICKS")
    </toolCall>
  </assistant>
  <user>
    <tools>
      ... same as before ...
    </tools>
    <toolResult id="slicePotato">
      {"result":"Potato#4"}
    </toolResult>
  </user>
  <assistant>
    <toolCall id="bakePotato">
      Oven_bake(Oven: "Oven#1", item: "Potato#4")
    </toolCall>
  </assistant>
  <user>
    <tools>
      ... same as before ...
    </tools>
    <toolResult id="bakePotato">
      {"result":"Fries#1"}
    </toolResult>
  </user>
  <assistant>
    <toolCall id="moreToolsCall">
      selectTools(tools: ["Fries_plate"])
    </toolCalls>
  </assistant>
  <user>
    <tools>
      ... same as before ...

      # Returns the plate with the fries placed upon it.
      Fries_plate(
        # The fries to put on the plate.
        Fries: string,
        # The place upon which to place the fries.
        plate: string,
      )
    </tools>
    <toolResult id="bakePotato">
      ok
    </toolResult>
  </user>
  <assistant>
    <toolCall id="plateFries">
      Fries_plate(Fries: "Fries#1", plate: "Plate#1")
    </toolCall>
  </assistant>
  <user>
    <tools>
      ... same as before (including Fries_plate) ...
    </tools>
    <toolResult id="plateFries">
      {"result": "Plate#2"}
    </toolResult>
  </user>
  <assistant>
    <toolCall id="return">
      returnToUser(fries: "Plate#2")
    </toolCall>
  </assistant>
</example>
