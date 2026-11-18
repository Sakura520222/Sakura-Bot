package contracts

// WikiOptions contains only public read filters. No LLM-controlled authorization flag.
type WikiOptions struct {
	Query, Channel, Slug, NodeID, Kind, After, Before string
	RevisionID, ContributorID                         string
	Page, PageSize                                    int
}

// WikiPublicTool is shared by the tool dispatcher and the web assistant allowlist.
func WikiPublicTool(name string) bool {
	switch name {
	case "wiki_search", "wiki_get_page", "wiki_get_related", "wiki_get_changes",
		"wiki_get_channel_profile", "wiki_get_revision_history", "wiki_get_contributors":
		return true
	}
	return false
}
