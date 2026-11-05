package contracts

// WikiOptions contains only public read filters. No LLM-controlled authorization flag.
type WikiOptions struct {
	Query, Channel, Slug, NodeID, Kind, After, Before string
	Page, PageSize                                    int
}
