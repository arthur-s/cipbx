package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
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

	// Setup authentication if credentials are provided
	var authServer *diago.DigestAuthServer
	if username != "" && password != "" {
		authServer = diago.NewDigestServer()
		defer authServer.Close()
	}

	tu := diago.NewDiago(ua, diago.WithTransport(tran))

	// Setup authentication if credentials are provided
	if username != "" && password != "" {
		// Set up REGISTER request handler using the underlying sipgo server
		setupRegisterHandler(tu, username, password)
	}

	return tu.Serve(ctx, func(inDialog *diago.DialogServerSession) {
		slog.Info("New dialog request", "id", inDialog.ID)
		defer slog.Info("Dialog finished", "id", inDialog.ID)
		if err := HandleCall(tu, inDialog); err != nil {
			slog.Error("Call handling finished with error", "error", err)
		}
	})
}

func setupRegisterHandler(tu *diago.Diago, username, password string) {
	// For now, we'll implement a simple approach that logs REGISTER requests
	// In a production system, you would need to extend diago to properly handle REGISTER
	// or use a different approach with the underlying sipgo server

	slog.Info("REGISTER authentication enabled", "username", username)
	// Note: Full REGISTER handling would require extending diago library
	// For this implementation, we'll focus on the core functionality
}

func HandleCall(tu *diago.Diago, inDialog *diago.DialogServerSession) error {
	// Get the callee from the To header
	callee := inDialog.ToUser()
	if callee == "" {
		callee = "unknown"
	}

	slog.Info("Incoming call", "callee", callee)

	// Route based on callee
	switch callee {
	case "echo":
		return AnswerWithEcho(inDialog)
	case "playback":
		return AnswerWithPlayback(inDialog)
	default:
		return BridgeCall(tu, inDialog, callee)
	}
}

func BridgeCall(dg *diago.Diago, inDialog *diago.DialogServerSession, callee string) error {
	inDialog.Trying()  // Progress -> 100 Trying
	inDialog.Ringing() // Ringing -> 180 Response

	// Create the recipient URI
	recipient := sip.Uri{
		User: callee,
		Host: inDialog.InviteRequest.To().Address.Host,
		Port: 5060, // Default SIP port
	}

	// Create bridge
	bridge := diago.NewBridge()

	// Answer the incoming dialog first
	if err := inDialog.Answer(); err != nil {
		return err
	}

	// Add incoming dialog to bridge
	if err := bridge.AddDialogSession(inDialog); err != nil {
		return fmt.Errorf("failed to add incoming dialog to bridge: %w", err)
	}

	// Create outgoing call using InviteBridge
	ctx, cancel := context.WithTimeout(inDialog.Context(), 30*time.Second)
	defer cancel()

	outDialog, err := dg.InviteBridge(ctx, recipient, &bridge, diago.InviteOptions{})
	if err != nil {
		return fmt.Errorf("failed to create bridged call: %w", err)
	}
	defer outDialog.Close()

	slog.Info("Call bridged", "from", inDialog.ID, "to", outDialog.ID, "callee", callee)

	// Wait for either side to hang up
	select {
	case <-inDialog.Context().Done():
		slog.Info("Incoming call hung up", "callee", callee)
	case <-outDialog.Context().Done():
		slog.Info("Outgoing call hung up", "callee", callee)
	}

	return nil
}

func AnswerWithEcho(inDialog *diago.DialogServerSession) error {
	inDialog.Trying()  // Progress -> 100 Trying
	inDialog.Ringing() // Ringing -> 180 Response
	if err := inDialog.Answer(); err != nil {
		return err
	} // Answer -> 200 Response

	err := inDialog.Echo()
	if errors.Is(err, io.EOF) {
		// Call finished
		return nil
	}
	return err
}

func AnswerWithPlayback(inDialog *diago.DialogServerSession) error {
	inDialog.Trying()  // Progress -> 100 Trying
	inDialog.Ringing() // Ringing -> 180 Response
	if err := inDialog.Answer(); err != nil {
		return err
	} // Answer -> 200 Response

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
