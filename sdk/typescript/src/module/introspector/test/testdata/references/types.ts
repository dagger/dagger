/**
 * Data
 */
export type Data = {
  /**
   * Item 1
   */
  item1: string
  item2: number
}

/**
 * Test Enum
 */
export enum TestEnum {
  /**
   * Field A
   */
  A = "a",

  /**
   * Field B
   */
  B = "b",
}

export const defaultEnum = TestEnum.B

export type STR = string