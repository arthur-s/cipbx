package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/arthur-s/cipbx/pkg/scenarios"
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
	transport  string
	expectByte uint8
)

type authCredential struct {
	Username string
	Password string
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
	rootCmd.Flags().StringVar(&transport, "transport", "udp", "Transport protocol (udp|tcp|tls|ws|wss)")
	rootCmd.Flags().Uint8Var(&expectByte, "expect", 0, "Expected byte value in RTP payload for validation (0 = disabled)")

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
		Transport: transport,
		BindHost:  listenAddr,
		BindPort:  port,
	}

	// Create underlying SIP server so we can hook REGISTER
	srv, _ := sipgo.NewServer(ua)
	tu := diago.NewDiago(ua, diago.WithServer(srv), diago.WithTransport(tran))

	// Setup REGISTER handler if credentials are provided
	if username != "" || password != "" {
		authCreds, realm, err := prepareAuthCredentials(username, password, listenAddr)
		if err != nil {
			return err
		}
		setupRegisterHandler(srv, realm, authCreds)
	}

	return tu.Serve(ctx, func(inDialog *diago.DialogServerSession) {
		slog.Info("New dialog request", "id", inDialog.ID)
		defer slog.Info("Dialog finished", "id", inDialog.ID)
		if err := HandleCall(tu, inDialog); err != nil {
			slog.Error("Call handling finished with error", "error", err)
		}
	})
}

func prepareAuthCredentials(userCSV, passCSV, fallbackRealm string) ([]authCredential, string, error) {
	if userCSV == "" || passCSV == "" {
		return nil, "", errors.New("both username and password must be provided")
	}

	users := splitAndTrim(userCSV)
	passwords := splitAndTrim(passCSV)
	if len(users) != len(passwords) {
		return nil, "", fmt.Errorf("username/password count mismatch: %d vs %d", len(users), len(passwords))
	}
	if len(users) == 0 {
		return nil, "", errors.New("no usernames provided")
	}

	creds := make([]authCredential, len(users))
	for i := range users {
		if users[i] == "" {
			return nil, "", fmt.Errorf("username at position %d is empty", i+1)
		}
		if passwords[i] == "" {
			return nil, "", fmt.Errorf("password for user %q is empty", users[i])
		}
		creds[i] = authCredential{Username: users[i], Password: passwords[i]}
	}

	realm := deriveRealm(creds, fallbackRealm)
	return creds, realm, nil
}

func splitAndTrim(csv string) []string {
	parts := strings.Split(csv, ",")
	res := make([]string, len(parts))
	for i, p := range parts {
		res[i] = strings.TrimSpace(p)
	}
	return res
}

func deriveRealm(creds []authCredential, fallback string) string {
	for _, cred := range creds {
		if at := strings.LastIndex(cred.Username, "@"); at > 0 && at < len(cred.Username)-1 {
			domain := cred.Username[at+1:]
			if domain != "" {
				return domain
			}
		}
	}
	return fallback
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
		if expectByte != 0 {
			return scenarios.AnswerWithEchoWithValidation(inDialog, timeoutCtx, expectByte)
		}
		return scenarios.AnswerWithEcho(inDialog, timeoutCtx)
	case "playback":
		return scenarios.AnswerWithPlayback(inDialog, timeoutCtx)
	default:
		return scenarios.BridgeCall(tu, inDialog, callee, timeoutCtx, registrarGet)
	}
}
