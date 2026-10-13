package clipvault

import (
	"errors"
	"slices"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
	"golang.org/x/net/html"
)

// Resource discovery reads a parsed selection but never serializes it back over
// the original representation. Failed snapshots leave the original URL intact.
func captureCompositeHTMLResources(cfg Config, raw []byte, load func(string, int) ([]byte, error), budget int, existing []domain.ContentAsset, issue func(string, string)) ([]domain.ContentResource, []domain.ContentAsset) {
	doc, err := compositeHTMLDocument(raw)
	if err != nil {
		return nil, nil
	}
	var resources []domain.ContentResource
	var assets []domain.ContentAsset
	seen, saved := map[string]bool{}, map[string]bool{}
	for _, asset := range existing {
		saved[asset.ID] = true
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Namespace != "" {
			return
		}
		if n.Type == html.ElementNode && slices.Contains([]string{"head", "script", "template", "iframe", "object", "embed"}, n.Data) {
			return
		}
		if n.Type == html.ElementNode && n.Data == "img" {
			for _, a := range n.Attr {
				if a.Namespace != "" || a.Key != "src" || strings.TrimSpace(a.Val) == "" || seen[a.Val] {
					continue
				}
				seen[a.Val] = true
				// Inline data already belongs to the original representation. A
				// preview/backup limit must not mark those saved bytes as missing.
				if isCompositeInlineReference(a.Val) {
					continue
				}
				if !cfg.Image.Enabled {
					issue("HTML Format", "images_disabled")
					continue
				}
				if len(resources) >= maxCompositeAssets || len(a.Val) > 1<<20 || budget <= len(a.Val)+64 {
					issue("HTML Format", "resource_limit")
					continue
				}
				data, err := load(a.Val, min(configuredCompositeImageLimit(cfg), budget-len(a.Val)-64))
				if err != nil {
					reason := "resource_unavailable"
					if errors.Is(err, ErrImageLimitExceeded) {
						reason = "resource_limit"
					}
					issue("HTML Format", reason)
					continue
				}
				media, err := compositeImageMediaType(data)
				if err != nil {
					reason := "resource_unavailable"
					if errors.Is(err, ErrImageLimitExceeded) {
						reason = "resource_limit"
					}
					issue("HTML Format", reason)
					continue
				}
				id := hashBytes(data)
				if !saved[id] && len(existing)+len(assets) >= maxCompositeAssets {
					issue("HTML Format", "resource_limit")
					continue
				}
				cost := len(a.Val) + len(id)
				if !saved[id] {
					cost += len(data)
				}
				if cost > budget {
					issue("HTML Format", "resource_limit")
					continue
				}
				resources = append(resources, domain.ContentResource{Source: a.Val, AssetID: id})
				if !saved[id] {
					assets = append(assets, domain.ContentAsset{ID: id, MediaType: media, Data: data})
					saved[id] = true
				}
				budget -= cost
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	slices.SortFunc(resources, func(a, b domain.ContentResource) int { return strings.Compare(a.Source, b.Source) })
	slices.SortFunc(assets, func(a, b domain.ContentAsset) int { return strings.Compare(a.ID, b.ID) })
	return resources, assets
}

func cachedCompositeImageLoader(load func(string, int) ([]byte, error)) func(string, int) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	cache := map[string]result{}
	return func(source string, limit int) ([]byte, error) {
		value, ok := cache[source]
		if !ok {
			value.data, value.err = load(source, limit)
			cache[source] = value
		}
		if len(value.data) > limit {
			return nil, ErrImageLimitExceeded
		}
		return value.data, value.err
	}
}
