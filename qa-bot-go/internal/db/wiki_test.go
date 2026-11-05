package db

import (
	"context"
	"database/sql"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"os"
	"testing"
)

func TestWikiIdentifiersAndURLs(t *testing.T) {
	for _, input := range []string{"@CHANNEL_12345", "https://t.me/channel_12345/", "channel_12345"} {
		if normalizeWikiChannel(input) != "channel_12345" {
			t.Fatal(input)
		}
	}
	if normalizeWikiChannel("-10012345") != "12345" {
		t.Fatal("numeric alias")
	}
	if wikiLike("a%b_c=") != "%a=%b=_c==%" {
		t.Fatal("LIKE wildcards not escaped")
	}
	for _, base := range []string{"", "javascript:alert(1)", "https://user:pass@example.com"} {
		if wikiAbsoluteURL(map[string]any{"public_base_url": base}, "/wiki/12345") != nil {
			t.Fatal("unsafe base", base)
		}
	}
	if wikiAbsoluteURL(map[string]any{"public_base_url": "https://wiki.example.com"}, "/wiki/12345") != "https://wiki.example.com/wiki/12345" {
		t.Fatal("absolute URL")
	}
}

// Python integration fixture creates this isolated schema and publication. Go performs
// no migrations/writes, validating the real cross-language publication contract.
func TestWikiSharedMySQL(t *testing.T) {
	dsn := os.Getenv("WIKI_MYSQL_DSN")
	if dsn == "" {
		t.Skip("No disposable Wiki MySQL fixture")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck
	repo := &WikiRepository{DB: database}
	ctx := context.Background()
	result, err := repo.ReadWiki(ctx, "wiki_search", contracts.WikiOptions{Query: "中文", Channel: "@channel_12345"})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("WIKI_EXPECT_REVOKED") == "1" {
		if result["total"] != 0 {
			t.Fatalf("revoked source leaked: %#v", result)
		}
		_, err = repo.ReadWiki(ctx, "wiki_get_page", contracts.WikiOptions{Channel: "12345", Slug: os.Getenv("WIKI_EXPECTED_PAGE")})
		if err == nil {
			t.Fatal("revoked page is readable")
		}
		return
	}
	if result["total"] != 1 {
		t.Fatalf("search result: %#v", result)
	}
	page, err := repo.ReadWiki(ctx, "wiki_get_page", contracts.WikiOptions{Channel: "-10012345", Slug: os.Getenv("WIKI_EXPECTED_PAGE")})
	if err != nil {
		t.Fatal(err)
	}
	if page["publication_id"] != os.Getenv("WIKI_EXPECTED_PUBLICATION") {
		t.Fatal("publication mismatch", page)
	}
	claims := page["claims"].([]any)
	evidence := claims[0].(map[string]any)["evidence"].([]any)[0].(map[string]any)
	if evidence["quote"] != nil || evidence["url"] != "https://t.me/channel_12345/1" {
		t.Fatal("source policy mismatch", evidence)
	}
	if page["wiki_url"] != "https://wiki.example.com/wiki/12345/page/"+os.Getenv("WIKI_EXPECTED_PAGE") {
		t.Fatal("Wiki URL mismatch")
	}
	profile, err := repo.ReadWiki(ctx, "wiki_get_channel_profile", contracts.WikiOptions{Channel: "12345"})
	if err != nil || profile["publication_id"] != page["publication_id"] {
		t.Fatal("profile mismatch", profile, err)
	}
	hidden, err := repo.ReadWiki(ctx, "wiki_search", contracts.WikiOptions{Channel: "private"})
	if err != nil || hidden["available"] != false {
		t.Fatal("private channel did not fail closed")
	}
}
