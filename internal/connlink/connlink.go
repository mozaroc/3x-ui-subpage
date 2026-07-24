// Package connlink builds "direct connection link" view models (the
// panel's own verbatim share-link string plus URLs for its QR code and
// downloadable config) shared by both the admin user-detail page and the
// public subscription page, so neither duplicates the other's rendering
// logic — both just build a []View from a subscriber's parsed link entries
// and link to the same public /sub/{subID}/link/{index}/... endpoints for
// the QR/config bytes themselves. This package never renders a link
// itself; a "link" here is the natural indexed unit (one canonical panel
// entry can correspond to more than one Host/CDN front for the same
// inbound, so an inbound id is not a reliable key).
package connlink

import (
	"fmt"

	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
)

// View is one direct connection link, ready to render.
type View struct {
	Index     int
	Tag       string
	Protocol  string
	Link      string
	QRPngURL  string
	QRSVGURL  string
	ConfigURL string
}

// Build renders one View per entry, in order. Link is always the entry's
// verbatim panel string, whether or not it parsed -- Tag/Protocol/ConfigURL
// degrade to empty for an entry this project's parser doesn't understand,
// but copy/QR still work off the raw string either way.
func Build(subID string, entries []tmplctx.Entry) []View {
	views := make([]View, 0, len(entries))
	for i, e := range entries {
		v := View{
			Index:    i,
			Link:     e.Raw,
			QRPngURL: fmt.Sprintf("/sub/%s/link/%d/qr.png", subID, i),
			QRSVGURL: fmt.Sprintf("/sub/%s/link/%d/qr.svg", subID, i),
		}
		if e.ParseErr == nil {
			v.Tag = e.Context.Remark
			v.Protocol = e.Context.Protocol
			v.ConfigURL = fmt.Sprintf("/sub/%s/link/%d/config.json", subID, i)
		}
		views = append(views, v)
	}
	return views
}
