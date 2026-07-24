package httpserver

import (
	"fmt"
	"time"

	"github.com/irazin/3x-ui-subpage/internal/apps"
	"github.com/irazin/3x-ui-subpage/internal/connlink"
	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// AppView is one application catalog entry with its deeplink already
// rendered against a specific subscriber's URL.
type AppView struct {
	Name         string
	Icon         string
	Description  string
	Download     string
	Deeplink     string
	Instructions string
	Platforms    []string
}

// SupportView carries the configured support contacts, shown verbatim on
// every subscription page.
type SupportView struct {
	Telegram string
	Discord  string
	Email    string
	Website  string
	Custom   string
}

// SubscriptionView is the data the HTML theme renders (as PageData.Data)
// for one subscriber.
type SubscriptionView struct {
	Username         string
	Status           string
	ExpiresAt        *time.Time
	TrafficUsed      int64
	TrafficTotal     int64
	TrafficRemaining int64
	SubscriptionURL  string
	QRPngURL         string
	DownloadXray     string
	DownloadXrayJSON string
	DownloadClash    string
	DownloadMihomo   string
	Apps             []AppView
	Support          SupportView
	Connections      []connlink.View
}

// buildSubscriptionURL builds the public, shareable subscription link for
// subID against the configured public base URL.
func buildSubscriptionURL(publicURL, subID string) string {
	return fmt.Sprintf("%s/sub/%s", publicURL, subID)
}

// buildSubscriptionView assembles the full page view model for sub,
// rendering every catalog app's deeplink against this subscriber's URL.
func buildSubscriptionView(sub domain.Subscription, catalogApps []apps.App, support SupportView, publicURL string, connections []connlink.View) SubscriptionView {
	subURL := buildSubscriptionURL(publicURL, sub.SubID)

	appViews := make([]AppView, 0, len(catalogApps))
	for _, a := range catalogApps {
		appViews = append(appViews, AppView{
			Name:         a.Name,
			Icon:         a.Icon,
			Description:  a.Description,
			Download:     a.Download,
			Deeplink:     apps.RenderDeeplink(a.Deeplink, subURL, sub.Username),
			Instructions: a.Instructions,
			Platforms:    a.Platforms,
		})
	}

	var total int64
	if sub.Traffic.Total > 0 {
		total = sub.Traffic.Total
	}

	return SubscriptionView{
		Username:         sub.Username,
		Status:           string(sub.Status),
		ExpiresAt:        sub.ExpiresAt,
		TrafficUsed:      sub.Traffic.Used(),
		TrafficTotal:     total,
		TrafficRemaining: sub.Traffic.Remaining(),
		SubscriptionURL:  subURL,
		QRPngURL:         fmt.Sprintf("/sub/%s/qr.png", sub.SubID),
		DownloadXray:     fmt.Sprintf("/sub/%s/xray", sub.SubID),
		DownloadXrayJSON: fmt.Sprintf("/sub/%s/xray.json", sub.SubID),
		DownloadClash:    fmt.Sprintf("/sub/%s/clash", sub.SubID),
		DownloadMihomo:   fmt.Sprintf("/sub/%s/mihomo", sub.SubID),
		Apps:             appViews,
		Support:          support,
		Connections:      connections,
	}
}
