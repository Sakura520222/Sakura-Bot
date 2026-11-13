export interface WikiPassage {
  claim: number;
  excerpt: string;
  excerptOccurrence?: number;
  range?: { start: number; end: number };
}

export function wikiPassage(claim: number, original: string, excerpt = ""): WikiPassage {
  const start = excerpt ? original.indexOf(excerpt) : -1;
  const range = start >= 0 && original.indexOf(excerpt, start + 1) === -1
    ? { start, end: start + excerpt.length }
    : undefined;
  return { claim, excerpt, range };
}

export function revisionText(original: string, passage: WikiPassage, replacement: string) {
  return passage.range
    ? original.slice(0, passage.range.start) + replacement + original.slice(passage.range.end)
    : replacement;
}
