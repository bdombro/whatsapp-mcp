package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
)

type fakePairBackend struct {
	mu           sync.Mutex
	paired       bool
	connected    bool
	jid          string
	startCalls   int32
	discCalls    int32
	qrItems      []whatsmeow.QRChannelItem
	startErr     error
	qrBlockUntil chan struct{}
}

func (f *fakePairBackend) IsPaired() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paired
}

func (f *fakePairBackend) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakePairBackend) PairedJID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.paired {
		return ""
	}
	return f.jid
}

func (f *fakePairBackend) StartQR(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	atomic.AddInt32(&f.startCalls, 1)
	f.mu.Lock()
	err := f.startErr
	items := append([]whatsmeow.QRChannelItem(nil), f.qrItems...)
	block := f.qrBlockUntil
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ch := make(chan whatsmeow.QRChannelItem, len(items))
	go func() {
		defer close(ch)
		for _, item := range items {
			if item.Event == whatsmeow.QRChannelSuccess.Event {
				f.mu.Lock()
				f.paired = true
				f.connected = true
				if f.jid == "" {
					f.jid = "15551234567@s.whatsapp.net"
				}
				f.mu.Unlock()
			}
			select {
			case ch <- item:
			case <-ctx.Done():
				return
			}
		}
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
			}
		}
	}()
	return ch, nil
}

func (f *fakePairBackend) DisconnectIfUnpaired() {
	if !f.IsPaired() {
		atomic.AddInt32(&f.discCalls, 1)
	}
}

func TestPairManager_AlreadyPairedNeverTriggersQR(t *testing.T) {
	backend := &fakePairBackend{
		paired:    true,
		connected: true,
		jid:       "15551234567@s.whatsapp.net",
	}
	pm := NewPairManager(context.Background(), backend, nil)

	st := pm.Status()
	if !st.Paired || !st.Connected || st.JID != "15551234567@s.whatsapp.net" {
		t.Fatalf("unexpected status: %+v", st)
	}

	qr := pm.GetQR(context.Background(), 50*time.Millisecond)
	if qr.Status != pairStatusAlreadyPaired || !qr.Paired || qr.QR != "" {
		t.Fatalf("unexpected QR response when already paired: %+v", qr)
	}
	if got := atomic.LoadInt32(&backend.startCalls); got != 0 {
		t.Fatalf("expected 0 StartQR calls when already paired, got %d", got)
	}

	cancel := pm.Cancel()
	if cancel.Status != pairStatusAlreadyPaired || !cancel.Paired {
		t.Fatalf("unexpected Cancel response when already paired: %+v", cancel)
	}
	if got := atomic.LoadInt32(&backend.discCalls); got != 0 {
		t.Fatalf("expected 0 disconnect calls when already paired, got %d", got)
	}
}

func TestPairManager_QRFlowAndTransitionToPaired(t *testing.T) {
	block := make(chan struct{})
	defer close(block)

	backend := &fakePairBackend{
		qrItems: []whatsmeow.QRChannelItem{
			{Event: "code", Code: "2@mock-qr-payload-secret", Timeout: 25 * time.Second},
		},
		qrBlockUntil: block,
	}
	pm := NewPairManager(context.Background(), backend, nil)

	st := pm.Status()
	if st.Paired || st.Connected || st.JID != "" {
		t.Fatalf("expected unpaired status, got %+v", st)
	}

	qr := pm.GetQR(context.Background(), 500*time.Millisecond)
	if qr.Status != pairStatusAvailable || qr.Paired || qr.QR != "2@mock-qr-payload-secret" || qr.ExpiresInS <= 0 {
		t.Fatalf("unexpected QR response: %+v", qr)
	}

	// Second call while active reuses the existing QR session rather than calling StartQR again.
	qr2 := pm.GetQR(context.Background(), 50*time.Millisecond)
	if qr2.QR != "2@mock-qr-payload-secret" || atomic.LoadInt32(&backend.startCalls) != 1 {
		t.Fatalf("expected cached QR on second call, got %+v (starts=%d)", qr2, backend.startCalls)
	}

	// Cancel stops unpaired QR session cleanly.
	cancel := pm.Cancel()
	if cancel.Status != pairStatusCancelled || cancel.Paired {
		t.Fatalf("expected cancelled response, got %+v", cancel)
	}
	if atomic.LoadInt32(&backend.discCalls) == 0 {
		t.Fatal("expected DisconnectIfUnpaired to be called on cancel")
	}
}

func TestPairManager_TimeoutAndOnPairedCallback(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		backend := &fakePairBackend{
			qrItems: []whatsmeow.QRChannelItem{
				{Event: whatsmeow.QRChannelTimeout.Event},
			},
		}
		pm := NewPairManager(context.Background(), backend, nil)
		qr := pm.GetQR(context.Background(), 500*time.Millisecond)
		if qr.Status != pairStatusTimeout || qr.Paired || qr.QR != "" {
			t.Fatalf("expected timeout response, got %+v", qr)
		}
	})

	t.Run("paired callback", func(t *testing.T) {
		var callbackCount int32
		backend := &fakePairBackend{
			qrItems: []whatsmeow.QRChannelItem{
				{Event: "code", Code: "2@qr-1", Timeout: 20 * time.Second},
				{Event: whatsmeow.QRChannelSuccess.Event},
			},
		}
		pm := NewPairManager(context.Background(), backend, func() {
			atomic.AddInt32(&callbackCount, 1)
		})
		qr := pm.GetQR(context.Background(), 500*time.Millisecond)
		if qr.Status != pairStatusAlreadyPaired || !qr.Paired || qr.QR != "" {
			t.Fatalf("expected already_paired after success, got %+v", qr)
		}
		if atomic.LoadInt32(&callbackCount) != 1 {
			t.Fatalf("expected onPaired callback once, got %d", callbackCount)
		}
		st := pm.Status()
		if !st.Paired || !st.Connected || st.JID != "15551234567@s.whatsapp.net" {
			t.Fatalf("unexpected paired status: %+v", st)
		}
	})
}

func TestPairRoutes_UnixSocketHTTP(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wapair")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, SocketFileName)

	block := make(chan struct{})
	defer close(block)
	backend := &fakePairBackend{
		qrItems: []whatsmeow.QRChannelItem{
			{Event: "code", Code: "2@socket-qr", Timeout: 20 * time.Second},
		},
		qrBlockUntil: block,
	}
	pm := NewPairManager(context.Background(), backend, nil)

	mux := http.NewServeMux()
	registerPairRoutes(mux, pm)
	ln, err := listenSocket(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	cli := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}

	resp, err := cli.Get("http://unix/api/pair/status")
	if err != nil {
		t.Fatal(err)
	}
	var st PairStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if st.Paired || st.Connected || st.JID != "" {
		t.Fatalf("unexpected status: %+v", st)
	}

	resp, err = cli.Get("http://unix/api/pair/qr")
	if err != nil {
		t.Fatal(err)
	}
	var qr PairQRResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if qr.Status != pairStatusAvailable || qr.QR != "2@socket-qr" {
		t.Fatalf("unexpected qr: %+v", qr)
	}

	resp, err = cli.Post("http://unix/api/pair/cancel", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on cancel, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Ensure status JSON never leaks Signal/key fields.
	resp, err = cli.Get("http://unix/api/pair/status")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, forbidden := range []string{"noise_key", "identity_key", "adv_secret", "registration_id"} {
		if _, ok := raw[forbidden]; ok {
			t.Fatalf("forbidden key %s leaked in /api/pair/status", forbidden)
		}
	}
	if _, err := cli.Post("http://unix/api/pair/status", "application/json", strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}
}
