/** Compose shared view styles with the fixed sidebar skin. */
export function appearanceClass(base: string | undefined, sidebar: string | undefined): string {
  return [base, sidebar].filter((value): value is string => value !== undefined && value !== '').join(' ')
}
