package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
)

const cleanupInterval = 10 * time.Minute

// cleanupRunner periodically removes finished torrents in a background
// goroutine. It tracks its own health so the /status handler can detect and
// restart it if it ever stops working.
type cleanupRunner struct {
	client *TransmissionClient
	api    *maxbot.Api

	// lastChatID is polled on each notification to decide where to send it.
	getChatID func() int64

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

func newCleanupRunner(client *TransmissionClient, api *maxbot.Api, getChatID func() int64) *cleanupRunner {
	return &cleanupRunner{
		client:    client,
		api:       api,
		getChatID: getChatID,
	}
}

// Start launches the periodic cleanup goroutine. It is idempotent: a second
// call while already running is a no-op.
func (r *cleanupRunner) Start(parent context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return
	}

	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.running = true

	go r.loop(ctx)
}

// loop runs cleanup iterations until the context is cancelled. A panic during
// any iteration is recovered and causes the loop to exit so that /status can
// detect a dead runner and restart it via Fix.
func (r *cleanupRunner) loop(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			// Mark unhealthy (not running) so /status can restart the runner.
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
			slog.Error("cleanup runner panicked and stopped", "recover", rec)
		}
	}()

	// Run once immediately, then on the interval.
	r.runOnce(ctx)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

// runOnce performs a single cleanup pass and notifies if anything was removed.
func (r *cleanupRunner) runOnce(ctx context.Context) {
	slog.Debug("Running periodic cleanup")
	cnt, err := Cleanup(ctx, r.client)
	if err != nil {
		slog.Error("Periodic cleanup error", "error", err)
		return
	}

	if cnt > 0 {
		slog.Info("Periodic cleanup removed torrents", "count", cnt)
		r.notify(ctx, fmt.Sprintf("Cleanup finished: removed %d torrent(s).", cnt))
	}
}

// notify sends a message to the last chat an allowed user sent a command from.
func (r *cleanupRunner) notify(ctx context.Context, text string) {
	chatID := r.getChatID()
	if chatID == 0 {
		slog.Warn("Cleanup notification skipped: no known chat from an allowed user")
		return
	}
	if err := r.api.Messages.Send(ctx, maxbot.NewMessage().SetChat(chatID).SetText(text)); err != nil {
		slog.Error("Failed to send cleanup notification", "error", err)
	}
}

// Fix restarts the runner if it stopped running (e.g. it panicked), returning
// whether a restart was needed.
func (r *cleanupRunner) Fix(ctx context.Context) (restarted bool) {
	r.mu.Lock()
	needsRestart := !r.running
	r.mu.Unlock()

	if !needsRestart {
		return false
	}

	slog.Warn("Cleanup runner unhealthy; restarting")
	if r.cancel != nil {
		r.cancel()
	}
	r.Start(ctx)
	return true
}
