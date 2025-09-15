package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/spf13/cobra"
)

var (
	listenAddr string
	port       int
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
	tu := diago.NewDiago(ua, diago.WithTransport(tran))

	return tu.Serve(ctx, func(inDialog *diago.DialogServerSession) {
		slog.Info("New dialog request", "id", inDialog.ID)
		defer slog.Info("Dialog finished", "id", inDialog.ID)
		if err := AnswerWithEcho(inDialog); err != nil {
			slog.Error("Record finished with error", "error", err)
		}
	})
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
