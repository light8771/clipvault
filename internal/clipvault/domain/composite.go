package domain

// CompositeContent preserves original supported clipboard representations.
// Resource snapshots are separate; preview generation never edits source data.
type CompositeContent struct {
	Representations []ContentRepresentation `json:"representations"`
	Assets          []ContentAsset          `json:"assets"`
	Kinds           []string                `json:"kinds"`
	Issues          []ContentIssue          `json:"issues"`
}
type ContentRepresentation struct {
	Format    string            `json:"format"`
	Data      []byte            `json:"data"`
	Resources []ContentResource `json:"resources,omitempty"`
}
type ContentResource struct {
	Source  string `json:"source"`
	AssetID string `json:"assetId"`
}
type ContentAsset struct {
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	Data      []byte `json:"data"`
}
type ContentIssue struct {
	Format string `json:"format"`
	Reason string `json:"reason"`
}

// Summary omits all recoverable payloads from list/search responses.
func (c *CompositeContent) Summary() *CompositeContent {
	if c == nil {
		return nil
	}
	out := &CompositeContent{Kinds: append([]string{}, c.Kinds...), Issues: append([]ContentIssue{}, c.Issues...), Assets: []ContentAsset{}, Representations: []ContentRepresentation{}}
	for _, r := range c.Representations {
		out.Representations = append(out.Representations, ContentRepresentation{Format: r.Format})
	}
	for _, a := range c.Assets {
		out.Assets = append(out.Assets, ContentAsset{ID: a.ID, MediaType: a.MediaType})
	}
	return out
}
func (item StoredItem) ContentKinds() []string {
	if item.RecordType != HistoryRecordContent {
		return []string{}
	}
	if item.Content != nil {
		return append([]string{}, item.Content.Kinds...)
	}
	return []string{item.Kind}
}
