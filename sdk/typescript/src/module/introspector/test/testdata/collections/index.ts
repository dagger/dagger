import { collection, delta, field, func, get, keys, object, CollectionDelta } from "../../../../../index.js"

@object()
export class Collections {
  @func()
  items(): Items { return new Items() }
}

@collection()
export class Items {
  @keys()
  names: string[] = ["b", "a"]

  @delta()
  selection?: CollectionDelta

  @field()
  prefix: string = "item:"

  @get()
  lookup(name: string): Item { return new Item(this.prefix + name) }

  @func()
  selected(): string[] { return this.names }
}

@object()
export class Item {
  @field()
  name: string

  constructor(name: string) { this.name = name }
}
