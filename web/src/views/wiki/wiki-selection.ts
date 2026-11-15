export interface WikiPassage {
  claim: number | null;
  excerpt: string;
  excerptOccurrence?: number;
  range?: { start: number; end: number };
}

export function wikiPassage(claim: number | null, original: string, excerpt = "", occurrence?: number): WikiPassage {
  const start = excerpt ? original.indexOf(excerpt) : -1;
  let offset = start;
  for (let i = 0; offset >= 0 && i < (occurrence ?? 0); i++) offset = original.indexOf(excerpt, offset + 1);
  const range = occurrence !== undefined && offset >= 0
    ? { start: offset, end: offset + excerpt.length }
    : start >= 0 && original.indexOf(excerpt, start + 1) === -1
    ? { start, end: start + excerpt.length }
    : undefined;
  return { claim, excerpt, range };
}

export function revisionText(original: string, passage: WikiPassage, replacement: string) {
  return passage.range
    ? original.slice(0, passage.range.start) + replacement + original.slice(passage.range.end)
    : replacement;
}
