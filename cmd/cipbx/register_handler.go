package main

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

func setupRegisterHandler(srv *sipgo.Server, user, pass string) {
	slog.Info("REGISTER authentication enabled", "username", user)

	authServer := diago.NewDigestServer()
	// Closed at process end; lifetime matches server

	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		// 1) Challenge/verify digest
		res, err := authServer.AuthorizeRequest(req, diago.DigestAuth{
			Username: user,
			Password: pass,
			Realm:    "cipbx",
			Expire:   30 * time.Second,
		})
		if err != nil || res.StatusCode != sip.StatusOK {
			if err != nil {
				slog.Info("REGISTER auth challenge", "error", err)
			}
			if tx != nil {
				tx.Respond(res)
			}
			return
		}

		// 2) Parse contact(s) and expiry
		to := req.To()
		aorUser := ""
		if to != nil {
			aorUser = to.Address.User
		}
		if aorUser == "" {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Bad Request", nil))
			return
		}

		// Determine requested expiry
		expiresSeconds := 3600
		if h := req.GetHeader("Expires"); h != nil {
			if v, err := strconv.Atoi(h.Value()); err == nil {
				expiresSeconds = v
			}
		}

		contact := req.Contact()
		if contact == nil {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Missing Contact", nil))
			return
		}
		if p := contact.Params; p != nil {
			if v, ok := p.Get("expires"); ok && v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					expiresSeconds = n
				}
			}
		}

		// Unregister if Contact: * and/or Expires 0
		if contact.Address.Wildcard || expiresSeconds == 0 {
			registrarSet(aorUser, sip.Uri{}, 0)
		} else {
			// NAT assist: prefer request source for host:port
			host, port, err := sip.ParseAddr(req.Source())
			stored := *contact.Address.Clone()
			if err == nil {
				stored.Host = host
				stored.Port = port
			}
			registrarSet(aorUser, stored, expiresSeconds)
		}

		// 3) Respond 200 OK with echoed Contact and Expires
		ok := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
		ok.AppendHeader(contact.Clone())
		ok.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(expiresSeconds)))
		tx.Respond(ok)
	})
}
