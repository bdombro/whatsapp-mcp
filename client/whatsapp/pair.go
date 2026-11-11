package whatsapp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
)

const (
	pairStatusWaiting       = "waiting"
	pairStatusAvailable     = "available"
	pairStatusTimeout       = "timeout"
	pairStatusAlreadyPaired = "already_paired"
	pairStatusCancelled     = "cancelled"
	defaultPairTimeout      = 3 * time.Minute
	defaultQRTTL            = 20 * time.Second
)

// PairStatusResponse is returned by GET /api/pair/status on daemon.sock.
// It exposes only minimal session status and never Signal or session keys.
type PairStatusResponse struct {
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
	JID       string `json:"jid"`
}

// PairQRResponse is returned by GET /api/pair/qr and POST /api/pair/cancel on daemon.sock.
type PairQRResponse struct {
	Status     string `json:"status"`
	Paired     bool   `json:"paired"`
	QR         string `json:"qr,omitempty"`
	ExpiresInS int    `json:"expires_in_s,omitempty"`
}

// PairBackend abstracts whatsmeow pairing/session calls so PairManager can be unit-tested without live WhatsApp sockets.
type PairBackend interface {
	IsPaired() bool
	IsConnected() bool
	PairedJID() string
	StartQR(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)
	DisconnectIfUnpaired()
}

type whatsmeowPairBackend struct {
	client *whatsmeow.Client
}

func (b *whatsmeowPairBackend) IsPaired() bool {
	return b.client != nil && b.client.Store != nil && b.client.Store.ID != nil
}

func (b *whatsmeowPairBackend) IsConnected() bool {
	return b.client != nil && b.client.IsConnected()
}

func (b *whatsmeowPairBackend) PairedJID() string {
	if !b.IsPaired() {
		return ""
	}
	return b.client.Store.ID.ToNonAD().String()
}

func (b *whatsmeowPairBackend) StartQR(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	qrChan, err := b.client.GetQRChannel(ctx)
	if err != nil {
		return nil, err
	}
	if err := b.client.Connect(); err != nil {
		return nil, err
	}
	return qrChan, nil
}

func (b *whatsmeowPairBackend) DisconnectIfUnpaired() {
	if b.client != nil && !b.IsPaired() {
		b.client.Disconnect()
	}
}

// PairManager coordinates bounded on-demand QR pairing over daemon.sock without logging QR payloads
// or disturbing already-paired sessions.
type PairManager struct {
	parentCtx   context.Context
	backend     PairBackend
	pairTimeout time.Duration
	onPaired    func()

	mu        sync.Mutex
	active    bool
	cancel    context.CancelFunc
	qrCode    string
	qrExpires time.Time
	status    string
	notifyCh  chan struct{}
}

// NewPairManager constructs a PairManager bound to parentCtx and backend.
func NewPairManager(parentCtx context.Context, backend PairBackend, onPaired func()) *PairManager {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	return &PairManager{
		parentCtx:   parentCtx,
		backend:     backend,
		pairTimeout: defaultPairTimeout,
		onPaired:    onPaired,
		status:      pairStatusWaiting,
		notifyCh:    make(chan struct{}),
	}
}

func (m *PairManager) broadcastLocked() {
	close(m.notifyCh)
	m.notifyCh = make(chan struct{})
}

// Status returns the current pairing and connection status with minimal identity.
func (m *PairManager) Status() PairStatusResponse {
	paired := m.backend.IsPaired()
	connected := m.backend.IsConnected()
	jid := m.backend.PairedJID()
	if paired {
		m.mu.Lock()
		m.qrCode = ""
		m.qrExpires = time.Time{}
		m.mu.Unlock()
	}
	return PairStatusResponse{
		Paired:    paired,
		Connected: connected,
		JID:       jid,
	}
}

// GetQR returns an active expiring QR payload when unpaired, starting a bounded QR session if needed.
// If already paired, it returns already_paired without starting QR or touching credentials.
func (m *PairManager) GetQR(ctx context.Context, waitTimeout time.Duration) PairQRResponse {
	if m.backend.IsPaired() {
		return PairQRResponse{
			Status: pairStatusAlreadyPaired,
			Paired: true,
		}
	}

	m.mu.Lock()
	if m.backend.IsPaired() {
		m.qrCode = ""
		m.qrExpires = time.Time{}
		m.mu.Unlock()
		return PairQRResponse{Status: pairStatusAlreadyPaired, Paired: true}
	}

	now := time.Now()
	if m.active && m.status == pairStatusAvailable && m.qrCode != "" && now.Before(m.qrExpires) {
		resp := PairQRResponse{
			Status:     pairStatusAvailable,
			Paired:     false,
			QR:         m.qrCode,
			ExpiresInS: remainingSeconds(m.qrExpires, now),
		}
		m.mu.Unlock()
		return resp
	}

	if !m.active {
		pairCtx, cancel := context.WithTimeout(m.parentCtx, m.pairTimeout)
		m.active = true
		m.cancel = cancel
		m.qrCode = ""
		m.qrExpires = time.Time{}
		m.status = pairStatusWaiting
		m.broadcastLocked()
		go m.runQRSession(pairCtx, cancel)
	}
	waitCh := m.notifyCh
	m.mu.Unlock()

	if waitTimeout > 0 {
		timer := time.NewTimer(waitTimeout)
		select {
		case <-waitCh:
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}

	if m.backend.IsPaired() {
		return PairQRResponse{Status: pairStatusAlreadyPaired, Paired: true}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backend.IsPaired() {
		m.qrCode = ""
		m.qrExpires = time.Time{}
		return PairQRResponse{Status: pairStatusAlreadyPaired, Paired: true}
	}
	now = time.Now()
	if m.status == pairStatusAvailable && m.qrCode != "" && now.Before(m.qrExpires) {
		return PairQRResponse{
			Status:     pairStatusAvailable,
			Paired:     false,
			QR:         m.qrCode,
			ExpiresInS: remainingSeconds(m.qrExpires, now),
		}
	}
	if m.status == pairStatusTimeout {
		return PairQRResponse{Status: pairStatusTimeout, Paired: false}
	}
	return PairQRResponse{Status: pairStatusWaiting, Paired: false}
}

// Cancel stops an active unpaired QR attempt. It refuses to disconnect an already-paired session.
func (m *PairManager) Cancel() PairQRResponse {
	if m.backend.IsPaired() {
		return PairQRResponse{Status: pairStatusAlreadyPaired, Paired: true}
	}

	m.mu.Lock()
	if m.backend.IsPaired() {
		m.qrCode = ""
		m.qrExpires = time.Time{}
		m.mu.Unlock()
		return PairQRResponse{Status: pairStatusAlreadyPaired, Paired: true}
	}
	cancel := m.cancel
	m.cancel = nil
	m.active = false
	m.qrCode = ""
	m.qrExpires = time.Time{}
	m.status = pairStatusCancelled
	m.broadcastLocked()
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.backend.DisconnectIfUnpaired()
	return PairQRResponse{Status: pairStatusCancelled, Paired: false}
}

func (m *PairManager) runQRSession(pairCtx context.Context, cancel context.CancelFunc) {
	defer cancel()
	qrChan, err := m.backend.StartQR(pairCtx)
	if err != nil {
		m.mu.Lock()
		if m.status != pairStatusCancelled {
			m.status = pairStatusTimeout
		}
		m.active = false
		m.cancel = nil
		m.qrCode = ""
		m.qrExpires = time.Time{}
		m.broadcastLocked()
		m.mu.Unlock()
		m.backend.DisconnectIfUnpaired()
		return
	}

	pairedNow := false
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			ttl := evt.Timeout
			if ttl <= 0 {
				ttl = defaultQRTTL
			}
			m.mu.Lock()
			if m.active && m.status != pairStatusCancelled {
				m.qrCode = evt.Code
				m.qrExpires = time.Now().Add(ttl)
				m.status = pairStatusAvailable
				m.broadcastLocked()
			}
			m.mu.Unlock()
		case whatsmeow.QRChannelSuccess.Event:
			pairedNow = true
			m.mu.Lock()
			m.active = false
			m.cancel = nil
			m.qrCode = ""
			m.qrExpires = time.Time{}
			m.status = pairStatusAlreadyPaired
			m.broadcastLocked()
			m.mu.Unlock()
			if m.onPaired != nil {
				m.onPaired()
			}
			return
		default:
			// Timeout, error, or client-outdated terminates this QR session without logging QR secrets.
			m.mu.Lock()
			m.active = false
			m.cancel = nil
			m.qrCode = ""
			m.qrExpires = time.Time{}
			if m.status != pairStatusCancelled {
				m.status = pairStatusTimeout
			}
			m.broadcastLocked()
			m.mu.Unlock()
			m.backend.DisconnectIfUnpaired()
			return
		}
	}

	if !pairedNow {
		m.mu.Lock()
		m.active = false
		m.cancel = nil
		m.qrCode = ""
		m.qrExpires = time.Time{}
		if m.backend.IsPaired() {
			m.status = pairStatusAlreadyPaired
		} else if m.status != pairStatusCancelled {
			m.status = pairStatusTimeout
		}
		m.broadcastLocked()
		m.mu.Unlock()
		if !m.backend.IsPaired() {
			m.backend.DisconnectIfUnpaired()
		}
	}
}

func remainingSeconds(expiresAt, now time.Time) int {
	rem := int(expiresAt.Sub(now).Seconds())
	if rem < 1 {
		return 1
	}
	return rem
}

// registerPairRoutes mounts the Unix-socket-only pairing endpoints on mux.
func registerPairRoutes(mux *http.ServeMux, pm *PairManager) {
	mux.HandleFunc("/api/pair/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(pm.Status())
	})

	mux.HandleFunc("/api/pair/qr", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(pm.GetQR(r.Context(), 2500*time.Millisecond))
	})

	mux.HandleFunc("/api/pair/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := pm.Cancel()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if resp.Paired {
			w.WriteHeader(http.StatusConflict)
		}
		json.NewEncoder(w).Encode(resp)
	})
}
