package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
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
	slug := os.Getenv("WIKI_EXPECTED_PAGE")
	pageOpts := contracts.WikiOptions{Channel: "12345", Slug: slug}
	result, err := repo.ReadWiki(ctx, "wiki_search", contracts.WikiOptions{Query: "中文", Channel: "@channel_12345"})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("WIKI_EXPECT_REVOKED") == "1" {
		if result["total"] != 0 {
			t.Fatalf("revoked source leaked: %#v", result)
		}
		for _, tool := range []string{"wiki_get_page", "wiki_get_revision_history", "wiki_get_contributors"} {
			if _, err := repo.ReadWiki(ctx, tool, pageOpts); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("revoked page is readable in %s: %v", tool, err)
			}
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
	if err != nil || profile["publication_id"] != page["publication_id"] || profile["description"] != "管理员设置的公开频道简介" {
		t.Fatal("profile mismatch", profile, err)
	}
	oldRevision := os.Getenv("WIKI_OLD_REVISION")
	history, err := repo.ReadWiki(ctx, "wiki_get_revision_history", pageOpts)
	if err != nil || history["total"] != 2 || history["current_revision_id"] != page["revision_id"] {
		t.Fatal("history mismatch", history, err)
	}
	pageOpts.RevisionID = oldRevision
	old, err := repo.ReadWiki(ctx, "wiki_get_page", pageOpts)
	if err != nil || old["revision_id"] != oldRevision || old["current_revision_id"] != page["revision_id"] ||
		old["wiki_url"] != page["wiki_url"].(string)+"?revision="+oldRevision {
		t.Fatal("old version mismatch", old, err)
	}
	if len(old["claims"].([]any)[0].(map[string]any)["community_notes"].([]map[string]any)) != 0 {
		t.Fatal("current notes leaked into old revision")
	}
	pageOpts.RevisionID = os.Getenv("WIKI_DRAFT_REVISION")
	if _, err := repo.ReadWiki(ctx, "wiki_get_page", pageOpts); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unpublished revision was accepted", err)
	}
	notes := claims[0].(map[string]any)["community_notes"].([]map[string]any)
	if len(notes) != 1 || notes[0]["text"] != "段落补充说明" || notes[0]["excerpt"] != "中文检索" || notes[0]["proposal_id"] != nil {
		t.Fatal("public note mismatch", notes)
	}
	contributors, err := repo.ReadWiki(ctx, "wiki_get_contributors", pageOpts)
	if err != nil {
		t.Fatal(err)
	}
	items := contributors["items"].([]map[string]any)
	pageOpts.ContributorID = os.Getenv("WIKI_CONTRIBUTOR")
	if os.Getenv("WIKI_EXPECT_ANONYMOUS") == "1" {
		if notes[0]["contributor"] != nil || len(items) != 0 {
			t.Fatal("anonymous contributor leaked")
		}
		if _, err := repo.ReadWiki(ctx, "wiki_get_contributors", pageOpts); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("anonymous contributor history is readable", err)
		}
	} else {
		author := notes[0]["contributor"].(map[string]any)
		if author["id"] != pageOpts.ContributorID || author["display_name"] != "公开贡献者" || author["username"] != nil ||
			len(items) != 1 || items[0]["username"] != nil || items[0]["total"] != int64(1) {
			t.Fatal("unexpected public contributor", author, items)
		}
		activity, err := repo.ReadWiki(ctx, "wiki_get_contributors", pageOpts)
		if err != nil {
			t.Fatal(err)
		}
		events := activity["items"].([]map[string]any)
		if len(events) != 1 || events[0]["kind"] != "annotation" || events[0]["wiki_url"] != page["wiki_url"] ||
			events[0]["telegram_id"] != nil || events[0]["username"] != nil {
			t.Fatal("unexpected contribution history", events)
		}
	}
	hidden, err := repo.ReadWiki(ctx, "wiki_search", contracts.WikiOptions{Channel: "private"})
	if err != nil || hidden["available"] != false {
		t.Fatal("private channel did not fail closed")
	}
}
