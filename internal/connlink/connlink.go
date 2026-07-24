// Package connlink builds per-inbound "direct connection link" view models
// (the rendered share-link URI plus URLs for its QR code and downloadable
// config) shared by both the admin user-detail page and the public
// subscription page, so neither duplicates the other's rendering logic —
// both just build a []View from a subscriber's matched clients and link to
// the same public /sub/{subID}/link/{inboundID}/... endpoints for the
// QR/config bytes themselves.
package connlink

import (
	"fmt"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// View is one inbound's direct connection link, ready to render.
type View struct {
	InboundID int
	Tag       string
	Protocol  string
	Link      string
	QRPngURL  string
	QRSVGURL  string
	ConfigURL string
}

// LinkBuilder renders a single matched client's share-link URI. Satisfied
// by *linkgen.Generator.
type LinkBuilder interface {
	BuildLink(mc domain.MatchedClient, profile string) (string, error)
}

// Build renders one View per client, using the subscriber's assigned Xray
// profile. A client whose link fails to render (e.g. an unsupported
// protocol, or a broken admin template) is skipped via onError rather than
// failing the whole page -- this is a display enhancement, not something
// that should be able to take down the rest of the page. onError may be
// nil.
func Build(subID string, clients []domain.MatchedClient, profile string, gen LinkBuilder, onError func(domain.MatchedClient, error)) []View {
	views := make([]View, 0, len(clients))
	for _, mc := range clients {
		link, err := gen.BuildLink(mc, profile)
		if err != nil {
			if onError != nil {
				onError(mc, err)
			}
			continue
		}

		views = append(views, View{
			InboundID: mc.InboundID,
			Tag:       mc.Remark,
			Protocol:  string(mc.Protocol),
			Link:      link,
			QRPngURL:  fmt.Sprintf("/sub/%s/link/%d/qr.png", subID, mc.InboundID),
			QRSVGURL:  fmt.Sprintf("/sub/%s/link/%d/qr.svg", subID, mc.InboundID),
			ConfigURL: fmt.Sprintf("/sub/%s/link/%d/config.json", subID, mc.InboundID),
		})
	}
	return views
}
