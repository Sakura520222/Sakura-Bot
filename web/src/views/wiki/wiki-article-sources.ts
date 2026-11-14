import type { WikiArticle, WikiReferences } from "../../api/wiki";

interface ArticleSource {
  title: string;
  url?: string;
  createdAt?: string;
  image?: boolean;
  quotes: string[];
}

export function articleSources(article: WikiArticle, sources: WikiReferences["sources"]) {
  const entries: ArticleSource[] = [];
  const registered = new Map<string, ArticleSource>();
  const originals = new Map(sources.map(source => [source.id, source]));
  const album = new Set(article.album_links?.map(item => item.url));
  function register(key: string, source: Omit<ArticleSource, "quotes">, quote?: string) {
    let entry = registered.get(key);
    if (!entry) {
      entry = { ...source, quotes: [] };
      registered.set(key, entry);
      entries.push(entry);
    }
    if (source.image) entry.image = true;
    if (quote && !entry.quotes.includes(quote)) entry.quotes.push(quote);
  }
  function original(id: string, url?: string, quote?: string) {
    const source = originals.get(id);
    url = source?.url || url || undefined;
    register(url || id, {
      url,
      title: url && album.has(url) ? "Telegram 相册原帖" : url ? "频道原帖" : "频道来源",
      image: source?.type === "image",
      createdAt: source?.created_at,
    }, quote);
  }
  for (const claim of article.claims) {
    for (const evidence of claim.evidence) {
      original(evidence.source_id, evidence.url, evidence.quote);
    }
    for (const source of claim.fact_check?.sources ?? []) {
      register(source.url, { url: source.url, title: source.title || source.url });
    }
  }
  for (const source of sources) original(source.id);
  for (const item of article.album_links ?? []) {
    register(item.url, { url: item.url, title: "Telegram 相册原帖" });
  }
  return entries;
}
