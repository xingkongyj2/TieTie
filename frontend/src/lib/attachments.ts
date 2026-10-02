/** Remove only the final extension, preserving dates, version dots and names. */
export function attachmentDisplayName(name: string): string {
  const at = name.lastIndexOf('.');
  return at > 0 ? name.slice(0, at) : name;
}
