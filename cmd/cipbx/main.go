package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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
	if username != "" && password != "" {
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
