import { usePluginData } from "@docusaurus/useGlobalData";

// Mirrors the model produced by plugins/dagger-api-reference/schema.js.
export type NamedKind = "core" | "enum" | "scalar" | "interface" | "object";

export type TypeRef =
  | { kind: "named"; name: string; named: NamedKind; isCore: boolean }
  | { kind: "list"; of: TypeRef }
  | { kind: "nonNull"; of: TypeRef };

export interface ApiArg {
  name: string;
  description: string;
  type: TypeRef;
  defaultValue?: string;
}

export interface ApiField {
  name: string;
  description: string;
  args: ApiArg[];
  type: TypeRef;
  deprecated: { reason: string } | null;
  experimental: { reason: string } | null;
  notes: string[];
}

export interface FieldRef {
  type: string;
  field: string;
  arg?: string;
}

export interface ApiType {
  name: string;
  description: string;
  implements: string[];
  fields: ApiField[];
  returnedBy: FieldRef[];
  argOf: FieldRef[];
}

export interface EnumValue {
  name: string;
  description: string;
  deprecated: string | null;
  enumValue: string | null;
}

export interface EnumType {
  name: string;
  description: string;
  values: EnumValue[];
}

export interface ApiModel {
  types: Record<string, ApiType>;
  enums: Record<string, EnumType>;
  coreTypes: string[];
}

// baseNamed unwraps a type to its underlying named token.
export function baseNamed(type: TypeRef): Extract<TypeRef, { kind: "named" }> {
  let t = type;
  while (t.kind !== "named") t = t.of;
  return t;
}

export function useApiModel(): ApiModel {
  return usePluginData("dagger-api-reference") as ApiModel;
}

export function useEnums(): Record<string, EnumType> {
  return useApiModel().enums;
}

export function useApiType(name: string): ApiType {
  const model = useApiModel();
  const t = model.types[name];
  if (!t) {
    throw new Error(
      `<ApiType name="${name}"> — no such core type in the schema model. ` +
        `Known types: ${Object.keys(model.types).join(", ")}`
    );
  }
  return t;
}

// typeSlug maps a type name to its reference page path segment, matching the
// kebab-case slugs used by the conceptual pages (CacheVolume -> cache-volume).
export function typeSlug(name: string): string {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1-$2")
    .toLowerCase();
}

export function typeHref(name: string): string {
  return `/reference/api/${typeSlug(name)}`;
}
