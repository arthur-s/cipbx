package main

import (
	"log/slog"
	"time"

	"github.com/emiago/sipgo/sip"
)

// Simple in-memory registrar
type registrationEntry struct {
	Contact sip.Uri
	Expire  time.Time
}

var registrar = struct {
	entries map[string]*registrationEntry
}{entries: map[string]*registrationEntry{}}

func registrarSet(aor string, contact sip.Uri, ttlSeconds int) {
	if ttlSeconds <= 0 {
		delete(registrar.entries, aor)
		slog.Info("Unregistered user", "aor", aor)
		return
	}
	registrar.entries[aor] = &registrationEntry{
		Contact: *contact.Clone(),
		Expire:  time.Now().Add(time.Duration(ttlSeconds) * time.Second),
	}
	slog.Info("Registered user", "aor", aor, "contact", contact.String(), "expires_in", ttlSeconds)
}

func registrarGet(aor string) (sip.Uri, bool) {
	e, ok := registrar.entries[aor]
	if !ok {
		return sip.Uri{}, false
	}
	if time.Now().After(e.Expire) {
		delete(registrar.entries, aor)
		return sip.Uri{}, false
	}
	return *e.Contact.Clone(), true
}
