package connlink

import (
	"errors"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
)

func TestBuild_OneViewPerEntryInOrder(t *testing.T) {
	entries := []tmplctx.Entry{
		{Raw: "vless://a", Context: tmplctx.ClientContext{Remark: "a", Protocol: "vless"}},
		{Raw: "trojan://b", Context: tmplctx.ClientContext{Remark: "b", Protocol: "trojan"}},
	}

	views := Build("tok-abc", entries)

	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
	if views[0].Index != 0 || views[0].Link != "vless://a" || views[0].Tag != "a" || views[0].Protocol != "vless" {
		t.Errorf("unexpected first view: %+v", views[0])
	}
	if views[1].Index != 1 || views[1].Link != "trojan://b" {
		t.Errorf("unexpected second view: %+v", views[1])
	}
	if views[0].QRPngURL != "/sub/tok-abc/link/0/qr.png" {
		t.Errorf("unexpected qr png url: %q", views[0].QRPngURL)
	}
	if views[0].QRSVGURL != "/sub/tok-abc/link/0/qr.svg" {
		t.Errorf("unexpected qr svg url: %q", views[0].QRSVGURL)
	}
	if views[0].ConfigURL != "/sub/tok-abc/link/0/config.json" {
		t.Errorf("unexpected config url: %q", views[0].ConfigURL)
	}
}

// TestBuild_UnparseableEntryStillShowsRawLink confirms an entry this
// project's parser doesn't understand still surfaces its verbatim string
// (copy/QR keep working) even though Tag/Protocol/ConfigURL degrade --
// never silently drop a link the panel actually gave a client just because
// our own parser doesn't recognize it.
func TestBuild_UnparseableEntryStillShowsRawLink(t *testing.T) {
	entries := []tmplctx.Entry{
		{Raw: "hysteria2://whatever", ParseErr: errors.New("unsupported scheme")},
	}

	views := Build("tok-abc", entries)

	if len(views) != 1 {
		t.Fatalf("expected 1 view even for an unparseable entry, got %d", len(views))
	}
	v := views[0]
	if v.Link != "hysteria2://whatever" {
		t.Errorf("expected raw link preserved, got %q", v.Link)
	}
	if v.Tag != "" || v.Protocol != "" {
		t.Errorf("expected Tag/Protocol to degrade to empty for an unparseable entry, got %+v", v)
	}
	if v.QRPngURL == "" || v.QRSVGURL == "" {
		t.Errorf("expected QR urls to still work off the raw link, got %+v", v)
	}
	if v.ConfigURL != "" {
		t.Errorf("expected ConfigURL to degrade to empty (needs parsed fields), got %q", v.ConfigURL)
	}
}

func TestBuild_EmptyEntriesYieldsEmptyViews(t *testing.T) {
	views := Build("tok-abc", nil)
	if len(views) != 0 {
		t.Fatalf("expected 0 views, got %d", len(views))
	}
}
