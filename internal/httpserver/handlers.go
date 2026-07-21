package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/domain"
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

// resolveProfile looks up the subscriber's assigned template profile,
// writing 500 and returning ok=false on failure.
func (s *Server) resolveProfile(w http.ResponseWriter, subID string) (string, bool) {
	profile, err := s.deps.Assignments.Resolve(subID)
	if err != nil {
		s.deps.Logger.Error("resolve profile assignment failed", "sub_id", subID, "err", err)
		http.Error(w, "failed to resolve template assignment", http.StatusInternalServerError)
		return "", false
	}
	return profile, true
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

func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}

	switch detectFormat(r.UserAgent()) {
	case formatHTML:
		s.renderHTML(w, sub)
	case formatClash:
		s.writeYAML(w, sub, s.deps.Clash, "clash.yaml")
	case formatMihomo:
		s.writeYAML(w, sub, s.deps.Mihomo, "mihomo.yaml")
	case formatHapp:
		s.writeRaw(w, sub, s.deps.Happ, "happ.json")
	case formatIncy:
		s.writeRaw(w, sub, s.deps.Incy, "incy.json")
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

	profile, ok := s.resolveProfile(w, sub.SubID)
	if !ok {
		return
	}

	out, err := s.deps.XrayJSON.Build(sub.Clients, profile)
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
	s.writeYAML(w, sub, s.deps.Clash, "clash.yaml")
}

func (s *Server) handleMihomo(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeYAML(w, sub, s.deps.Mihomo, "mihomo.yaml")
}

func (s *Server) handleHapp(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeRaw(w, sub, s.deps.Happ, "happ.json")
}

func (s *Server) handleIncy(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveOrFail(w, r)
	if !ok {
		return
	}
	s.writeRaw(w, sub, s.deps.Incy, "incy.json")
}

func (s *Server) writeXrayLinks(w http.ResponseWriter, sub domain.Subscription) {
	profile, ok := s.resolveProfile(w, sub.SubID)
	if !ok {
		return
	}

	body, err := s.deps.LinkGen.BuildSubscription(sub.Clients, profile)
	if err != nil {
		s.deps.Logger.Error("render xray links failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to render subscription", http.StatusInternalServerError)
		return
	}
	setSubscriptionUserinfo(w, sub)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (s *Server) writeYAML(w http.ResponseWriter, sub domain.Subscription, gen YAMLGenerator, filename string) {
	profile, ok := s.resolveProfile(w, sub.SubID)
	if !ok {
		return
	}

	out, err := gen.Build(sub.Clients, profile)
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

func (s *Server) writeRaw(w http.ResponseWriter, sub domain.Subscription, gen RawGenerator, filename string) {
	profile, ok := s.resolveProfile(w, sub.SubID)
	if !ok {
		return
	}

	out, err := gen.Build(sub.Clients, profile)
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

	view := buildSubscriptionView(sub, catalogApps, support, s.deps.PublicURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.deps.Theme.Render(w, view); err != nil {
		s.deps.Logger.Error("render theme failed", "sub_id", sub.SubID, "err", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
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
