import {  object, func, Directory } from "@dagger.io/dagger"

@object()
export class Impl {
  @func()
  str: string
  
  @func()
  strList: string[]

  @func()
  int: number

  @func()
  intList: number[]

  @func()
  bool: boolean

  @func()
  boolList: boolean[]

  @func()
  obj: Directory

  @func()
  objList: Directory[]

  @func()
  others: OtherImpl[]

  @func()
  otherIfaces: LocalOtherIface[]

  constructor(strs: string[], ints: number[], bools: boolean[], dirs: Directory[]) {
    this.str = strs[0];
    this.strList = strs;
    this.int = ints[0];
    this.intList = ints;
    this.bool = bools[0];
    this.boolList = bools;
    this.obj = dirs[0];
    this.objList = dirs;
    this.others = []
    this.otherIfaces = []
  }

  private copy(): Impl {
    const cpy = new Impl(this.strList, this.intList, this.boolList, this.objList)
    cpy.others = this.others
    cpy.otherIfaces = this.otherIfaces

    return cpy
  }

  @func()
  void(): void {
    return 
  } 

  @func()
  withStr(str: string): Impl {
    // Workaround to make selfIfaceList works since it expect to create a copy
    const newThis = this.copy()
    newThis.str = str;
    return newThis;
  }

  @func()
  withOptionalStr(str?: string): Impl {
    if (str) {
      this.str = str;
    }
    return this;
  }

  @func()
  withStrList(strs: string[]): Impl {
    this.strList = strs;
    return this;
  }

  @func()
  withInt(int: number): Impl {
    this.int = int;
    return this;
  }

  @func()
  withIntList(ints: number[]): Impl {
    this.intList = ints;
    return this;
  }

  @func()
  withBool(bool: boolean): Impl {
    this.bool = bool;
    return this;
  }

  @func()
  withBoolList(bools: boolean[]): Impl {
    this.boolList = bools;
    return this;
  }

  @func()
  withObj(obj: Directory): Impl {
    this.obj = obj;
    return this;
  }

  @func()
  withOptionalObj(obj?: Directory): Impl {
    if (obj) {
      this.obj = obj;
    }
    return this;
  }

  @func()
  withObjList(objs: Directory[]): Impl {
    this.objList = objs;
    return this;
  }

  @func()
  selfIface(): Impl {
    return this.withStr(this.str + "self")
  }

  @func()
  selfIfaceList(): Impl[] {
    return [
      this.withStr(this.str + "self1"),
      this.withStr(this.str + "self2"),
    ]
  }

  @func()
  otherIface(): OtherImpl {
    return new OtherImpl(this.str + "other")
  }

  @func()
  staticOtherIfaceList(): OtherImpl[] {
    return [
      new OtherImpl(this.str + "other1"),
      new OtherImpl(this.str + "other2"),
    ]
  }

  @func()
  withOtherIface(other: OtherImpl): Impl {
    this.others.push(other)
    return this
  }

  @func()
  dynamicOtherIfaceList(): OtherImpl[] {
    return this.others
  }

  @func()
  withOtherIfaceByIface(other: LocalOtherIface): Impl {
    this.otherIfaces.push(other)
    return this
  }

  @func()
  dynamicOtherIfaceByIfaceList(): LocalOtherIface[] {
    return this.otherIfaces
  }
}

@object()
export class OtherImpl {
  _foo: string

  constructor(foo: string) {
    this._foo = foo;
  }

  @func()
  async foo(): Promise<string> {
    return this._foo
  }
}

export interface LocalOtherIface {
  foo: () => Promise<string>
}