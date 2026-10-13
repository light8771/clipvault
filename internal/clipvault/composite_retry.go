package clipvault

import (
	"bytes"
	"slices"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
)

// Reuse immutable representations only within one clipboard sequence. A
// producer can render HTML and RTF on different attempts; a later unavailable
// handle must not erase bytes already captured from that same event.
func mergeCompositeRetry(cfg Config, previous, current *ClipboardSnapshot) *ClipboardSnapshot {
	if previous == nil || previous.Content == nil || current == nil || current.Content == nil {
		return current
	}
	content := *current.Content
	content.Representations = append([]domain.ContentRepresentation(nil), content.Representations...)
	content.Assets = append([]domain.ContentAsset(nil), content.Assets...)
	content.Issues = append([]domain.ContentIssue(nil), content.Issues...)
	changed := false
	for _, candidate := range []struct{ representation, source string }{
		{compositeHTML, "HTML Format"}, {compositeWeChat, compositeWeChat}, {compositeQQ, compositeQQ}, {compositeRTF, "Rich Text Format"}, {compositePNG, "image/png"}, {compositeFiles, "CF_HDROP"},
	} {
		index := slices.IndexFunc(previous.Content.Representations, func(r domain.ContentRepresentation) bool { return r.Format == candidate.representation })
		if index < 0 {
			continue
		}
		prior := previous.Content.Representations[index]
		currentIndex := slices.IndexFunc(content.Representations, func(r domain.ContentRepresentation) bool { return r.Format == candidate.representation })
		recoveredReason := "data_unavailable"
		if currentIndex >= 0 {
			latest := content.Representations[currentIndex]
			// A transient second fetch must not discard a complete backup of
			// identical source bytes. Never replace new bytes or newer resources.
			if !bytes.Equal(prior.Data, latest.Data) || len(prior.Resources) <= len(latest.Resources) ||
				slices.ContainsFunc(previous.Content.Issues, func(i domain.ContentIssue) bool { return i.Format == candidate.source }) ||
				!slices.ContainsFunc(content.Issues, func(i domain.ContentIssue) bool {
					return i.Format == candidate.source && i.Reason == "resource_unavailable"
				}) ||
				slices.ContainsFunc(latest.Resources, func(r domain.ContentResource) bool { return !slices.Contains(prior.Resources, r) }) {
				continue
			}
			recoveredReason = "resource_unavailable"
			content.Representations[currentIndex] = prior
		} else {
			if !slices.ContainsFunc(content.Issues, func(i domain.ContentIssue) bool {
				return i.Format == candidate.source && i.Reason == "data_unavailable"
			}) {
				continue
			}
			content.Representations = append(content.Representations, prior)
		}
		if candidate.representation == compositeHTML || candidate.representation == compositeWeChat || candidate.representation == compositeQQ {
			for _, asset := range previous.Content.Assets {
				needed := slices.ContainsFunc(previous.Content.Representations[index].Resources, func(r domain.ContentResource) bool { return r.AssetID == asset.ID })
				if needed && !slices.ContainsFunc(content.Assets, func(a domain.ContentAsset) bool { return a.ID == asset.ID }) {
					content.Assets = append(content.Assets, asset)
				}
			}
		}
		content.Issues = slices.DeleteFunc(content.Issues, func(i domain.ContentIssue) bool {
			return i.Format == candidate.source && i.Reason == recoveredReason
		})
		// A saved representation can itself be incomplete (e.g. a missing HTML
		// image). Keep its diagnostics even though its handle is now recovered.
		for _, issue := range previous.Content.Issues {
			if issue.Format == candidate.source && !slices.Contains(content.Issues, issue) {
				content.Issues = append(content.Issues, issue)
			}
		}
		if candidate.representation == compositeFiles && !slices.ContainsFunc(content.Issues, func(i domain.ContentIssue) bool { return i.Format == "CF_HDROP" }) {
			// Complete physical files also recover the shell's virtual-file
			// alternative; a missing thumbnail is optional in this case.
			content.Issues = slices.DeleteFunc(content.Issues, func(i domain.ContentIssue) bool {
				return i.Format == "FileContents" && i.Reason == "virtual_files_unsupported" ||
					i.Format == "image/png" && (i.Reason == "data_unavailable" || i.Reason == "decode_failed" || i.Reason == "resource_limit")
			})
		}
		changed = true
	}
	if !changed {
		return current
	}
	slices.SortFunc(content.Representations, func(a, b domain.ContentRepresentation) int { return strings.Compare(a.Format, b.Format) })
	slices.SortFunc(content.Assets, func(a, b domain.ContentAsset) int { return strings.Compare(a.ID, b.ID) })
	text, kinds, err := compositeTextAndKinds(&content)
	if err != nil || cfg.MaxTextChars > 0 && textRuneLen(text) > cfg.MaxTextChars {
		return current
	}
	content.Kinds = kinds
	_, size, err := validateComposite(&content)
	if err != nil {
		return current
	}
	out := *current
	out.Content, out.Text = &content, text
	out.Kind, out.Format = domain.KindComposite, domain.FormatComposite
	out.ByteLen, out.CharLen = size, textRuneLen(text)
	out.SHA256 = compositeFingerprint(&content)
	return &out
}
