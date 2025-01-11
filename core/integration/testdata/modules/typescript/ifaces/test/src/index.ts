import { Directory, func, object } from "@dagger.io/dagger";

export interface CustomIface {
  void(): Promise<void>;

  str: () => Promise<string>;
  withStr: (str: string) => Promise<CustomIface>;
  withOptionalStr: (str?: string) => Promise<CustomIface>;

  strList: () => Promise<string[]>;
  withStrList: (strs: string[]) => Promise<CustomIface>;

  int: () => Promise<number>;
  withInt: (int: number) => Promise<CustomIface>;

  intList: () => Promise<number[]>;
  withIntList: (ints: number[]) => Promise<CustomIface>;

  bool: () => Promise<boolean>;
  withBool: (bool: boolean) => Promise<CustomIface>;
  boolList: () => Promise<boolean[]>;
  withBoolList: (bools: boolean[]) => Promise<CustomIface>;

  obj: () => Promise<Directory>;
  withObj: (obj: Directory) => Promise<CustomIface>;
  withOptionalObj: (obj?: Directory) => Promise<CustomIface>;

  objList: () => Promise<Directory[]>;
  withObjList: (objs: Directory[]) => Promise<CustomIface>;

  selfIface: () => Promise<CustomIface>;
  selfIfaceList: () => Promise<CustomIface[]>;

  otherIface: () => Promise<OtherIface>;
  staticOtherIfaceList: () => Promise<OtherIface[]>;
  withOtherIface: (other: OtherIface) => Promise<CustomIface>;
  dynamicOtherIfaceList: () => Promise<OtherIface[]>;

  withOtherIfaceByIface(other: OtherIface): Promise<CustomIface>;
  dynamicOtherIfaceByIfaceList: () => Promise<OtherIface[]>;
}

export interface OtherIface {
  foo: () => Promise<string>;
}

@object()
export class Test {
  @func()
  IfaceField?: CustomIface;

  @func()
  IfaceFieldNeverSet: CustomIface;

  IfaceFieldPrivateField: CustomIface;

  @func()
  IfaceListField: CustomIface[];

  @func()
  OtherIfaceListField: OtherIface[];

  @func()
  async void(ifaceArg: CustomIface): Promise<void> {
    return ifaceArg.void();
  }

  @func()
  async str(ifaceArg: CustomIface): Promise<string> {
    return await ifaceArg.str();
  }

  @func()
  async withStr(ifaceArg: CustomIface, strArg: string): Promise<CustomIface> {
    return ifaceArg.withStr(strArg);
  }

  @func()
  async withOptionalStr(ifaceArg: CustomIface, strArg?: string): Promise<CustomIface> {
    return ifaceArg.withOptionalStr(strArg);
  }

  @func()
  async strList(ifaceArg: CustomIface): Promise<string[]> {
    return ifaceArg.strList();
  }

  @func()
  async withStrList(ifaceArg: CustomIface, strListArg: string[]): Promise<CustomIface> {
    return ifaceArg.withStrList(strListArg);
  }

  @func()
  async int(ifaceArg: CustomIface): Promise<number> {
    return ifaceArg.int();
  }

  @func()
  async withInt(ifaceArg: CustomIface, intArg: number): Promise<CustomIface> {
    return ifaceArg.withInt(intArg);
  }

  @func()
  async intList(ifaceArg: CustomIface): Promise<number[]> {
    return ifaceArg.intList();
  }

  @func()
  async withIntList(ifaceArg: CustomIface, intListArg: number[]): Promise<CustomIface> {
    return ifaceArg.withIntList(intListArg);
  }

  @func()
  async bool(ifaceArg: CustomIface): Promise<boolean> {
    return ifaceArg.bool();
  }

  @func()
  async withBool(ifaceArg: CustomIface, boolArg: boolean): Promise<CustomIface> {
    return ifaceArg.withBool(boolArg);
  }

  @func()
  async boolList(ifaceArg: CustomIface): Promise<boolean[]> {
    return ifaceArg.boolList();
  }

  @func()
  async withBoolList(ifaceArg: CustomIface, boolListArg: boolean[]): Promise<CustomIface> {
    return ifaceArg.withBoolList(boolListArg);
  }

  @func()
  async obj(ifaceArg: CustomIface): Promise<Directory> {
    return ifaceArg.obj();
  }

  @func()
  async withObj(ifaceArg: CustomIface, objArg: Directory): Promise<CustomIface> {
    return ifaceArg.withObj(objArg);
  }

  @func()
  async withOptionalObj(ifaceArg: CustomIface, objArg?: Directory): Promise<CustomIface> {
    return ifaceArg.withOptionalObj(objArg);
  }

  @func()
  async objList(ifaceArg: CustomIface): Promise<Directory[]> {
    return ifaceArg.objList();
  }

  @func()
  async withObjList(ifaceArg: CustomIface, objListArg: Directory[]): Promise<CustomIface> {
    return ifaceArg.withObjList(objListArg);
  }

  @func()
  async selfIface(ifaceArg: CustomIface): Promise<CustomIface> {
    return ifaceArg.selfIface();
  }

  @func()
  async selfIfaceList(ifaceArg: CustomIface): Promise<CustomIface[]> {
    return ifaceArg.selfIfaceList();
  }

  @func()
  async otherIface(ifaceArg: CustomIface): Promise<OtherIface> {
    return ifaceArg.otherIface();
  }

  @func()
  async staticOtherIfaceList(ifaceArg: CustomIface): Promise<OtherIface[]> {
    return ifaceArg.staticOtherIfaceList();
  }

  @func()
  async withOtherIface(ifaceArg: CustomIface, otherArg: OtherIface): Promise<CustomIface> {
    return ifaceArg.withOtherIface(otherArg);
  }

  @func()
  async dynamicOtherIfaceList(ifaceArg: CustomIface): Promise<OtherIface[]> {
    return ifaceArg.dynamicOtherIfaceList();
  }

  @func()
  async withOtherIfaceByIface(ifaceArg: CustomIface, otherArg: OtherIface): Promise<CustomIface> {
    return ifaceArg.withOtherIfaceByIface(otherArg);
  }

  @func()
  async dynamicOtherIfaceByIfaceList(ifaceArg: CustomIface): Promise<OtherIface[]> {
    return ifaceArg.dynamicOtherIfaceByIfaceList();
  }

  @func()
  async ifaceListArgs(ifaces: CustomIface[], otherIfaces: OtherIface[]): Promise<string[]> {
    const strs: string[] = [];
    for (const iface of ifaces) {
      const str = await iface.str();

      strs.push(str);
    }

    for (const otherIface of otherIfaces) {
      const str = await otherIface.foo();

      strs.push(str);
    }

    return strs;
  }

  @func()
  withIface(iface: CustomIface): Test {
    this.IfaceField = iface;

    return this;
  }

  @func()
  withOptionalIface(iface?: CustomIface): Test {
    if (iface !== undefined) {
      this.IfaceField = iface;
    }

    return this;
  }

  @func()
  withIfaceList(ifaces: CustomIface[]): Test {
    this.IfaceListField = ifaces;

    return this;
  }

  @func()
  withOtherIfaceList(otherIfaces: OtherIface[]): Test {
    this.OtherIfaceListField = otherIfaces;

    return this;
  }

  @func()
  withPrivateIface(iface: CustomIface): Test {
    this.IfaceFieldPrivateField = iface;

    return this;
  }

  @func()
  async parentIfaceFields(): Promise<string[]> {
    const strs: string[] = [];

    console.log({ listField: this.IfaceListField })

    if (this.IfaceField !== undefined) {
      const str = await this.IfaceField.str();
      strs.push(str);
    }

    if (this.IfaceFieldPrivateField !== undefined) {
      const str = await this.IfaceFieldPrivateField.str();
      strs.push(str);
    }

    if (this.IfaceListField !== undefined) {
      for (const iface of this.IfaceListField) {
        const str = await iface.str();
        strs.push(str);
      }
    }

    if (this.OtherIfaceListField !== undefined) {
      for (const otherIface of this.OtherIfaceListField) {
        const str = await otherIface.foo();
        strs.push(str);
      }
    }

    return strs;
  }

  @func()
  returnCustomObj(ifaces: CustomIface[], otherIfaces: OtherIface[]): CustomObj {
    return {
      Iface: ifaces[0],
      IfaceList: ifaces,
      Other: {
        Iface: ifaces[0],
        IfaceList: ifaces,
      },
      OtherList: [
        {
          Iface: ifaces[0],
          IfaceList: ifaces,
        },
      ],
    };
  }
}

export type CustomObj = {
  Iface: CustomIface;
  IfaceList: CustomIface[];
  Other: OtherCustomObj;
  OtherList: OtherCustomObj[];
};

export type OtherCustomObj = {
  Iface: CustomIface;
  IfaceList: CustomIface[];
};
