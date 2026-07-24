package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/connlink"
	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
	"github.com/irazin/3x-ui-subpage/internal/qrcode"
	"github.com/irazin/3x-ui-subpage/internal/resolver"
)

// resolveOrFail looks up subID, writing 400/404/502 as appropriate and
// returning ok=false if the caller should stop handling the request.
func (s *Server) resolveOrFail(w http.ResponseWriter, r *http.Request) (domain.Subscription, bool) {
	subID := chi.URLParam(r, "subID")
	if !validSubID(subID) {
		http.Error(w, "invalid subscription id", http.StatusBadRequest)
		return domain.Subscription{}, false
	}

	sub, err := s.deps.Resolver.Resolve(r.Context(), subID)
	switch {
	case err == nil:
		return sub, true
	case errors.Is(err, resolver.ErrNotFound):
		http.NotFound(w, r)
	default:
		s.deps.Logger.Error("resolve subscription failed", "sub_id", subID, "err", err)
		http.Error(w, "upstream panel error", http.StatusBadGateway)
	}
	return domain.Subscription{}, false
}

// resolveProfile looks up the subscriber's assigned template profile for
// format, writing 500 and returning ok=false on failure.
func (s *Server) resolveProfile(w http.ResponseWriter, subID, format string) (string, bool) {
	profile, err := s.deps.Assignments.Resolve(subID, format)
	if err != nil {
		s.deps.Logger.Error("resolve profile assignment failed", "sub_id", subID, "format", format, "err", err)
		http.Error(w, "failed to resolve template assignment", http.StatusInternalServerError)
		return "", false
	}
	return profile, true
}

// parsedContexts parses every one of sub's canonical share links and
// returns only the ones that parsed successfully, logging a warning for
// any that didn't -- an entry this project's parser doesn't yet understand
// (a future panel feature) is dropped from generation rather than failing
// the whole config, since the raw subscription body and direct-link list
// never parse at all and are unaffected either way.
func (s *Server) parsedContexts(sub domain.Subscription) []tmplctx.ClientContext {
	entries := tmplctx.ParseEntries(sub.Links)
	contexts := make([]tmplctx.ClientContext, 0, len(entries))
	for _, e := range entries {
		if e.ParseErr != nil {
			s.deps.Logger.Warn("parse share link failed", "sub_id", sub.SubID, "err", e.ParseErr)
			continue
		}
		contexts = append(contexts, e.Context)
	}
	return contexts
}

// setSubscriptionUserinfo sets the de-facto standard header many clients
// (v2rayNG, NekoBox, ...) read to display quota/expiry without parsing the
// subscription body itself.
func setSubscriptionUserinfo(w http.ResponseWriter, sub domain.Subscription) {
	var expire int64
	if sub.ExpiresAt != nil {
		expire = sub.ExpiresAt.Unix()
	}
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf(
		"upload=%d; download=%d; total=%d; expire=%d",
		sub.Traffic.Up, sub.Traffic.Down, sub.Traffic.Total, expire,
	))
}

// writeRoutingHeaders sets the Routing-Enable / Routing response headers
// Happ/Incy read to auto-install a subscriber's Routing Profile -- mirrors
// upstream 3x-ui's own ApplyCommonHeaders, scoped per-subscriber. scheme is
// "happ" or "incy". When routing is disabled (or never configured), only
// Routing-Enable: false is set -- no Routing header at all, so the client
// behaves exactly as it did before this feature existed.
func (s *Server) writeRoutingHeaders(w http.ResponseWriter, sub domain.Subscription, scheme string) {
	enabled, profile, err := s.deps.Routing.Get(sub.SubID)
	if err != nil {
		s.deps.Logger.Warn("resolve routing profile failed", "sub_id", sub.SubID, "err", err)
		return
	}
	w.Header().Set("Routing-Enable", strconv.FormatBool(enabled))
	if !enabled {
		return
	}
	name := sub.Username
	if name == "" {
		name = sub.SubID
	}
	link, err := profile.EncodeDeepLink(scheme, "onadd", name)
	if err != nil {
		s.deps.Logger.Warn("encode routing deep link failed", "sub_id", sub.SubID, "err", err)
		return
	}
	w.Header().Set("Routing", link)
}

func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}

	switch detectFormat(r.UserAgent()) {
	case formatHTML:
		s.renderHTML(w, sub)
	case formatClash:
		s.writeYAML(w, sub, s.deps.Clash, "clash", "clash.yaml")
	case formatMihomo:
		s.writeYAML(w, sub, s.deps.Mihomo, "mihomo", "mihomo.yaml")
	case formatIncy:
		s.writeRaw(w, sub, s.deps.Incy, "incy", "incy.json")
	default:
		s.writeXrayLinks(w, sub)
	}
}

func (s *Server) handleXray(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeXrayLinks(w, sub)
}

func (s *Server) handleXrayJSON(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}

	profile, ok := s.resolveProfile(w, sub.SubID, "xray_json")
	if !ok {
		return
	}

	out, err := s.deps.XrayJSON.Build(s.parsedContexts(sub), profile)
	if err != nil {
		s.deps.Logger.Error("render xray json config failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to render config", http.StatusInternalServerError)
		return
	}

	setSubscriptionUserinfo(w, sub)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.json"`)
	_, _ = w.Write([]byte(out))
}

func (s *Server) handleClash(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeYAML(w, sub, s.deps.Clash, "clash", "clash.yaml")
}

func (s *Server) handleMihomo(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeYAML(w, sub, s.deps.Mihomo, "mihomo", "mihomo.yaml")
}

func (s *Server) handleHapp(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeRoutingHeaders(w, sub, "happ")
	s.writeRaw(w, sub, s.deps.Happ, "happ", "happ.json")
}

func (s *Server) handleIncy(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeRoutingHeaders(w, sub, "incy")
	s.writeRaw(w, sub, s.deps.Incy, "incy", "incy.json")
}

// writeXrayLinks writes 3x-ui's own canonical share links, verbatim,
// joined and base64-encoded exactly like the classic subscription body --
// no profile, no template, no reconstruction. This is also exactly where
// the real Happ app lands (it isn't routed to the custom "happ" JSON
// format -- see detectFormat's doc comment), so it gets the "happ" routing
// headers too.
func (s *Server) writeXrayLinks(w http.ResponseWriter, sub domain.Subscription) {
	s.writeRoutingHeaders(w, sub, "happ")
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(sub.Links, "\n")))
	setSubscriptionUserinfo(w, sub)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (s *Server) writeYAML(w http.ResponseWriter, sub domain.Subscription, gen YAMLGenerator, format, filename string) {
	profile, ok := s.resolveProfile(w, sub.SubID, format)
	if !ok {
		return
	}

	out, err := gen.Build(s.parsedContexts(sub), profile)
	if err != nil {
		s.deps.Logger.Error("render yaml config failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to render config", http.StatusInternalServerError)
		return
	}
	setSubscriptionUserinfo(w, sub)
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = w.Write([]byte(out))
}

func (s *Server) writeRaw(w http.ResponseWriter, sub domain.Subscription, gen RawGenerator, format, filename string) {
	profile, ok := s.resolveProfile(w, sub.SubID, format)
	if !ok {
		return
	}

	out, err := gen.Build(s.parsedContexts(sub), profile)
	if err != nil {
		s.deps.Logger.Error("render raw config failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to render config", http.StatusInternalServerError)
		return
	}
	setSubscriptionUserinfo(w, sub)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = w.Write([]byte(out))
}

func (s *Server) renderHTML(w http.ResponseWriter, sub domain.Subscription) {
	catalogApps, err := s.deps.Apps.List()
	if err != nil {
		s.deps.Logger.Error("load app catalog failed", "err", err)
		http.Error(w, "failed to load applications", http.StatusInternalServerError)
		return
	}

	support := SupportView{
		Telegram: s.deps.Support.Telegram,
		Discord:  s.deps.Support.Discord,
		Email:    s.deps.Support.Email,
		Website:  s.deps.Support.Website,
		Custom:   s.deps.Support.Custom,
	}

	connections := connlink.Build(sub.SubID, tmplctx.ParseEntries(sub.Links))

	view := buildSubscriptionView(sub, catalogApps, support, s.deps.PublicURL, connections)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.deps.Theme.Render(w, view); err != nil {
		s.deps.Logger.Error("render theme failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// handleLink writes the raw share-link URI (e.g. "vless://...") for a
// single link index, verbatim -- 3x-ui's own canonical string, byte for
// byte, never reconstructed.
func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	link, ok := s.findLink(w, r, sub)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(link))
}

func (s *Server) handleLinkQRPNG(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	link, ok := s.findLink(w, r, sub)
	if !ok {
		return
	}

	png, err := qrcode.GeneratePNG(link, s.qrOptions())
	if err != nil {
		s.deps.Logger.Error("generate connection qr png failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to generate qr code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func (s *Server) handleLinkQRSVG(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	link, ok := s.findLink(w, r, sub)
	if !ok {
		return
	}

	svg, err := qrcode.GenerateSVG(link, s.qrOptions())
	if err != nil {
		s.deps.Logger.Error("generate connection qr svg failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to generate qr code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte(svg))
}

// handleLinkConfig writes a single-client full xray-core JSON config for
// one link index, downloadable independently of the whole-subscription
// config. Parses just this one link on demand.
func (s *Server) handleLinkConfig(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	link, ok := s.findLink(w, r, sub)
	if !ok {
		return
	}

	cc, err := tmplctx.ParseShareLink(link)
	if err != nil {
		s.deps.Logger.Warn("parse share link for config download failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "no downloadable config available for this link", http.StatusUnprocessableEntity)
		return
	}

	profile, ok := s.resolveProfile(w, sub.SubID, "xray_json")
	if !ok {
		return
	}

	out, err := s.deps.XrayJSON.Build([]tmplctx.ClientContext{cc}, profile)
	if err != nil {
		s.deps.Logger.Error("render single-link xray json config failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to render config", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.json"`)
	_, _ = w.Write([]byte(out))
}

// findLink parses the {index} URL param and bounds-checks it against sub's
// canonical links, writing 400/404 as appropriate.
func (s *Server) findLink(w http.ResponseWriter, r *http.Request, sub domain.Subscription) (string, bool) {
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil || index < 0 || index >= len(sub.Links) {
		if err != nil {
			http.Error(w, "invalid link index", http.StatusBadRequest)
		} else {
			http.NotFound(w, r)
		}
		return "", false
	}
	return sub.Links[index], true
}

func (s *Server) handleQRPNG(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	content := buildSubscriptionURL(s.deps.PublicURL, sub.SubID)

	png, err := qrcode.GeneratePNG(content, s.qrOptions())
	if err != nil {
		s.deps.Logger.Error("generate qr png failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to generate qr code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func (s *Server) handleQRSVG(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	content := buildSubscriptionURL(s.deps.PublicURL, sub.SubID)

	svg, err := qrcode.GenerateSVG(content, s.qrOptions())
	if err != nil {
		s.deps.Logger.Error("generate qr svg failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to generate qr code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte(svg))
}

func (s *Server) qrOptions() qrcode.Options {
	return qrcode.Options{
		Size:       s.deps.QRDefaults.Size,
		Margin:     s.deps.QRDefaults.Margin,
		Foreground: s.deps.QRDefaults.Foreground,
		Background: s.deps.QRDefaults.Background,
	}
}

type apiSubscriptionResponse struct {
	Username         string `json:"username"`
	Status           string `json:"status"`
	ExpiresAt        *int64 `json:"expiresAt,omitempty"`
	TrafficUsed      int64  `json:"trafficUsed"`
	TrafficTotal     int64  `json:"trafficTotal"`
	TrafficRemaining int64  `json:"trafficRemaining"`
	SubscriptionURL  string `json:"subscriptionUrl"`
}

func (s *Server) handleAPISubscription(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}

	var expiresAt *int64
	if sub.ExpiresAt != nil {
		v := sub.ExpiresAt.Unix()
		expiresAt = &v
	}

	resp := apiSubscriptionResponse{
		Username:         sub.Username,
		Status:           string(sub.Status),
		ExpiresAt:        expiresAt,
		TrafficUsed:      sub.Traffic.Used(),
		TrafficTotal:     sub.Traffic.Total,
		TrafficRemaining: sub.Traffic.Remaining(),
		SubscriptionURL:  buildSubscriptionURL(s.deps.PublicURL, sub.SubID),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleAPIApplications(w http.ResponseWriter, r *http.Request) {
	catalogApps, err := s.deps.Apps.List()
	if err != nil {
		s.deps.Logger.Error("load app catalog failed", "err", err)
		http.Error(w, "failed to load applications", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(catalogApps)
}

func (s *Server) handleStaticAsset(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")

	found, err := s.deps.Theme.ServeStatic(w, r, path)
	if err != nil {
		s.deps.Logger.Error("serve static asset failed", "path", path, "err", err)
		http.Error(w, "failed to load asset", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
