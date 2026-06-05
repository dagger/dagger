import { usePluginData } from "@docusaurus/useGlobalData";

// Mirrors the model produced by plugins/dagger-api-reference/schema.js.
export type TypeRef =
  | { kind: "named"; name: string; isCore: boolean }
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
}

export interface ApiType {
  name: string;
  description: string;
  implements: string[];
  fields: ApiField[];
}

export interface ApiModel {
  types: Record<string, ApiType>;
  coreTypes: string[];
}

export function useApiModel(): ApiModel {
  return usePluginData("dagger-api-reference") as ApiModel;
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
