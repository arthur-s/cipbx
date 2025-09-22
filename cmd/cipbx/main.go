package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/spf13/cobra"
)

var (
	listenAddr string
	port       int
	username   string
	password   string
	timeout    int
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

func main() {
	SetupLogger()

	var rootCmd = &cobra.Command{
		Use:   "cipbx server",
		Short: "A simple CI PBX server",
		Long:  "A simple CI PBX server",
		Run: func(cmd *cobra.Command, args []string) {
			err := startServer()
			if err != nil {
				slog.Error("PBX finished with error", "error", err)
			}
		},
	}

	// Set up flags with default values
	rootCmd.Flags().StringVarP(&listenAddr, "listen", "l", "127.0.0.1", "IP address to listen on")
	rootCmd.Flags().IntVarP(&port, "port", "p", 5090, "Port to listen on")
	rootCmd.Flags().StringVarP(&username, "username", "u", "", "Username for authentication (optional)")
	rootCmd.Flags().StringVarP(&password, "password", "w", "", "Password for authentication (optional)")
	rootCmd.Flags().IntVarP(&timeout, "timeout", "t", 0, "Call timeout in seconds (0 = no timeout)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func SetupLogger() {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetLogLoggerLevel(lvl)
	media.RTPDebug = os.Getenv("RTP_DEBUG") == "true"
	media.RTCPDebug = os.Getenv("RTCP_DEBUG") == "true"
	sip.SIPDebug = os.Getenv("SIP_DEBUG") == "true"
	sip.TransactionFSMDebug = os.Getenv("SIP_TRANSACTION_DEBUG") == "true"
}

func startServer() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Setup our main transaction user
	ua, _ := sipgo.NewUA()
	defer ua.Close()

	tran := diago.Transport{
		Transport: "udp",
		BindHost:  listenAddr,
		BindPort:  port,
	}

	// Create underlying SIP server so we can hook REGISTER
	srv, _ := sipgo.NewServer(ua)
	tu := diago.NewDiago(ua, diago.WithServer(srv), diago.WithTransport(tran))

	// Setup authentication if credentials are provided
	if username != "" && password != "" {
		// Set up REGISTER request handler using the underlying sipgo server
		setupRegisterHandler(srv, username, password)
	}

	return tu.Serve(ctx, func(inDialog *diago.DialogServerSession) {
		slog.Info("New dialog request", "id", inDialog.ID)
		defer slog.Info("Dialog finished", "id", inDialog.ID)
		if err := HandleCall(tu, inDialog); err != nil {
			slog.Error("Call handling finished with error", "error", err)
		}
	})
}

func setupRegisterHandler(srv *sipgo.Server, user, pass string) {
	slog.Info("REGISTER authentication enabled", "username", user)

	authServer := diago.NewDigestServer()
	// Closed implicitly at process end; no hook here since srv lifetime == process

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
			// No Contact: treat as error
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

		// Unregister if Contact: * and Expires 0
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
			// Store registration
			registrarSet(aorUser, stored, expiresSeconds)
		}

		// 3) Respond 200 OK with echoed Contact and Expires
		ok := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
		ok.AppendHeader(contact.Clone())
		ok.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(expiresSeconds)))
		tx.Respond(ok)
	})
}

func HandleCall(tu *diago.Diago, inDialog *diago.DialogServerSession) error {
	// Get the callee from the To header
	callee := inDialog.ToUser()
	if callee == "" {
		callee = "unknown"
	}

	slog.Info("Incoming call", "callee", callee)

	// Set up timeout if specified
	var timeoutCtx context.Context
	var cancelTimeout context.CancelFunc

	if timeout > 0 {
		timeoutCtx, cancelTimeout = context.WithTimeout(inDialog.Context(), time.Duration(timeout)*time.Second)
		defer cancelTimeout()

		// Start timeout goroutine
		go func() {
			<-timeoutCtx.Done()
			if timeoutCtx.Err() == context.DeadlineExceeded {
				slog.Info("Call timeout reached, hanging up", "callee", callee, "timeout", timeout)
				inDialog.Hangup(inDialog.Context())
			}
		}()
	}

	// Route based on callee
	switch callee {
	case "echo":
		return AnswerWithEcho(inDialog, timeoutCtx)
	case "playback":
		return AnswerWithPlayback(inDialog, timeoutCtx)
	default:
		return BridgeCall(tu, inDialog, callee, timeoutCtx)
	}
}

func BridgeCall(dg *diago.Diago, inDialog *diago.DialogServerSession, callee string, timeoutCtx context.Context) error {
	inDialog.Trying()  // 100 Trying
	inDialog.Ringing() // 180 Ringing

	// Resolve the recipient URI (prefer registrar entry)
	var recipient sip.Uri
	if reg, ok := registrarGet(callee); ok {
		recipient = reg
	} else {
		recipient = sip.Uri{
			User: callee,
			Host: inDialog.InviteRequest.To().Address.Host,
			Port: 5060,
		}
	}

	// Prepare bridge
	bridge := diago.NewBridge()

	// Place the outbound leg first
	ctx, cancel := context.WithTimeout(inDialog.Context(), 30*time.Second)
	defer cancel()

	outDialog, err := dg.InviteBridge(ctx, recipient, &bridge, diago.InviteOptions{})
	if err != nil {
		// Callee not reachable; reject inbound appropriately
		_ = inDialog.Respond(sip.StatusTemporarilyUnavailable, "Temporarily Unavailable", nil)
		return fmt.Errorf("failed to create bridged call: %w", err)
	}
	defer outDialog.Close()

	// Now answer the inbound leg and add it to the bridge
	if err := inDialog.Answer(); err != nil {
		return err
	}
	if err := bridge.AddDialogSession(inDialog); err != nil {
		return fmt.Errorf("failed to add incoming dialog to bridge: %w", err)
	}

	slog.Info("Call bridged", "from", inDialog.ID, "to", outDialog.ID, "callee", callee)

	// Handle optional timeout safely
	var timeoutDone <-chan struct{}
	if timeoutCtx != nil {
		timeoutDone = timeoutCtx.Done()
	}

	// Wait for either side to hang up or timeout
	select {
	case <-timeoutDone:
		slog.Info("Bridge call timeout reached", "callee", callee)
		return nil
	case <-inDialog.Context().Done():
		slog.Info("Incoming call hung up", "callee", callee)
	case <-outDialog.Context().Done():
		slog.Info("Outgoing call hung up", "callee", callee)
	}

	return nil
}

func AnswerWithEcho(inDialog *diago.DialogServerSession, timeoutCtx context.Context) error {
	inDialog.Trying()  // Progress -> 100 Trying
	inDialog.Ringing() // Ringing -> 180 Response
	if err := inDialog.Answer(); err != nil {
		return err
	} // Answer -> 200 Response

	// Handle timeout for echo calls
	if timeoutCtx != nil {
		select {
		case <-timeoutCtx.Done():
			slog.Info("Echo call timeout reached")
			return nil
		default:
		}
	}

	err := inDialog.Echo()
	if errors.Is(err, io.EOF) {
		// Call finished
		return nil
	}
	return err
}

func AnswerWithPlayback(inDialog *diago.DialogServerSession, timeoutCtx context.Context) error {
	inDialog.Trying()  // Progress -> 100 Trying
	inDialog.Ringing() // Ringing -> 180 Response
	if err := inDialog.Answer(); err != nil {
		return err
	} // Answer -> 200 Response

	// Handle timeout for playback calls
	if timeoutCtx != nil {
		select {
		case <-timeoutCtx.Done():
			slog.Info("Playback call timeout reached")
			return nil
		default:
		}
	}

	// Create playback instance
	pb, err := inDialog.PlaybackCreate()
	if err != nil {
		slog.Error("Failed to create playback", "error", err)
		return err
	}

	// Open the playback file
	playfile, err := os.Open("demo-echodone.wav")
	if err != nil {
		slog.Error("Failed to open playback file", "error", err)
		return err
	}
	defer playfile.Close()

	slog.Info("Playing a file", "file", "demo-echodone.wav")
	_, err = pb.Play(playfile, "audio/wav")
	return err
}
