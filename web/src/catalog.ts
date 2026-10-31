export const catalogPageSize = 24;

// Read one extra item to know if another page exists, including exact-size final pages.
export function catalogPage<T>(items: T[]) {
  return { items: items.slice(0, catalogPageSize), hasMore: items.length > catalogPageSize };
}
