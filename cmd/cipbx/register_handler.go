package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

func setupRegisterHandler(srv *sipgo.Server, realm string, creds []authCredential) {
	slog.Info("REGISTER authentication enabled", "accounts", len(creds), "realm", realm)

	authServer := diago.NewDigestServer()
	// Closed at process end; lifetime matches server
	credentialMap := make(map[string]string, len(creds))
	for _, c := range creds {
		credentialMap[c.Username] = c.Password
	}

	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		// 1) Challenge/verify digest
		digestSpec, rejectRes, err := buildDigestSpec(req, realm, credentialMap)
		if err != nil {
			slog.Info("REGISTER auth preprocessing", "error", err)
		}
		if rejectRes != nil {
			if tx != nil {
				tx.Respond(rejectRes)
			}
			return
		}

		res, err := authServer.AuthorizeRequest(req, *digestSpec)
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

func buildDigestSpec(req *sip.Request, realm string, credentials map[string]string) (*diago.DigestAuth, *sip.Response, error) {
	challenge := diago.DigestAuth{Realm: realm, Expire: 30 * time.Second}

	authHeader := req.GetHeader("Authorization")
	if authHeader == nil {
		return &challenge, nil, nil
	}

	cred, err := digest.ParseCredentials(authHeader.Value())
	if err != nil {
		return nil, sip.NewResponseFromRequest(req, sip.StatusBadRequest, "Bad Authorization", nil), fmt.Errorf("parse credentials: %w", err)
	}

	pass, ok := credentials[cred.Username]
	if !ok {
		chal := digest.Challenge{
			Realm:     realm,
			Nonce:     fmt.Sprintf("%x", sip.GenerateTagN(32)),
			Algorithm: "MD5",
		}
		res := sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unknown User", nil)
		res.AppendHeader(sip.NewHeader("WWW-Authenticate", chal.String()))
		return nil, res, errors.New("unknown username")
	}

	challenge.Username = cred.Username
	challenge.Password = pass
	return &challenge, nil, nil
}
