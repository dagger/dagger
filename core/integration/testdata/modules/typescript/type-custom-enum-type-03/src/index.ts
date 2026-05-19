import { func, object } from "@dagger.io/dagger"

/**
 * Enum for Status
 */
export enum Status {
  /**
   * Active status
   */
  Active = "ACTIVE value",

  /**
   * Inactive status
   */
  Inactive = "INACTIVE value",

  /**
   * Weird status
   */
  WEIRD = "WEIRD",
}

@object()
export class Test {
  @func()
  status: Status

  constructor(status: Status = Status.Inactive) {
    this.status = status
  }

  @func()
  fromStatus(status: Status): string {
    return status as string
  }

  @func()
  fromStatusOpt(status?: Status): string {
	if (status) {
		return status as string
	}
    return ""
  }

  @func()
  toStatus(status: string): Status {
    return status as Status
  }
}
