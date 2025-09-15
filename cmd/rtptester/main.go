package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/emiago/diago/media"
	"github.com/spf13/cobra"
)

var (
	localIP    string
	localPort  int
	remoteIP   string
	remotePort int
	codecName  string
	debug      bool
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "rtp-echo",
		Short: "RTP Echo Tester",
		Long:  "RTP Echo Tester that sends and receives RTP packets",
		Run: func(cmd *cobra.Command, args []string) {
			runApplication()
		},
	}

	// Set up flags with default values
	rootCmd.Flags().StringVarP(&localIP, "local-ip", "l", "0.0.0.0", "Local RTP IP address")
	rootCmd.Flags().IntVarP(&localPort, "local-port", "p", 5004, "Local RTP port")
	rootCmd.Flags().StringVarP(&remoteIP, "remote-ip", "r", "127.0.0.1", "Remote RTP IP address")
	rootCmd.Flags().IntVarP(&remotePort, "remote-port", "P", 5005, "Remote RTP port")
	rootCmd.Flags().StringVarP(&codecName, "codec", "c", "PCMA", "Codec to use (PCMA, PCMU, opus)")
	rootCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")

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
}

func runApplication() {
	SetupLogger()

	logger := slog.Default()

	// Set debug logging if enabled
	if debug {
		media.RTPDebug = true
		media.RTCPDebug = true
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		slog.SetDefault(logger)
	}

	// Parse codec
	codec, err := parseCodec(codecName)
	if err != nil {
		logger.Error("Invalid codec", "error", err, "codec", codecName)
		os.Exit(1)
	}

	// Parse addresses
	localAddr := net.ParseIP(localIP)
	if localAddr == nil {
		logger.Error("Invalid local IP address", "ip", localIP)
		os.Exit(1)
	}

	remoteAddr := net.ParseIP(remoteIP)
	if remoteAddr == nil {
		logger.Error("Invalid remote IP address", "ip", remoteIP)
		os.Exit(1)
	}

	logger.Info("Starting RTP Echo Tester",
		"local_ip", localIP,
		"local_port", localPort,
		"remote_ip", remoteIP,
		"remote_port", remotePort,
		"codec", codec.Name,
		"payload_type", codec.PayloadType)

	// Create context for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Start the RTP echo server
	if err := startRTPEchoServer(ctx, logger, localAddr, localPort, remoteAddr, remotePort, codec); err != nil {
		logger.Error("RTP echo server failed", "error", err)
		os.Exit(1)
	}
}

func parseCodec(codecName string) (media.Codec, error) {
	switch strings.ToUpper(codecName) {
	case "PCMA":
		return media.CodecAudioAlaw, nil
	case "PCMU":
		return media.CodecAudioUlaw, nil
	case "OPUS":
		return media.CodecAudioOpus, nil
	default:
		return media.Codec{}, fmt.Errorf("unsupported codec: %s. Supported: PCMA, PCMU, opus", codecName)
	}
}

func startRTPEchoServer(ctx context.Context, logger *slog.Logger, localIP net.IP, localPort int, remoteIP net.IP, remotePort int, codec media.Codec) error {
	// Create UDP listener for receiving
	rtpAddr := &net.UDPAddr{IP: localIP, Port: localPort}
	rtpConn, err := net.ListenUDP("udp", rtpAddr)
	if err != nil {
		return fmt.Errorf("failed to create RTP listener: %w", err)
	}
	defer rtpConn.Close()

	// Create separate UDP connection for sending
	sendConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: remoteIP, Port: remotePort})
	if err != nil {
		return fmt.Errorf("failed to create send connection: %w", err)
	}
	defer sendConn.Close()

	logger.Info("RTP Echo Server started",
		"listen_addr", rtpAddr.String(),
		"send_addr", fmt.Sprintf("%s:%d", remoteIP, remotePort),
		"codec", codec.Name)

	// Start the echo in a goroutine
	echoDone := make(chan error, 1)
	go func() {
		logger.Info("Starting simple RTP echo...")
		logger.Info("Waiting for RTP packets on", "listen_addr", rtpAddr.String())
		logger.Info("Will echo to", "send_addr", fmt.Sprintf("%s:%d", remoteIP, remotePort))

		buffer := make([]byte, 1500) // Standard MTU size
		packetCount := 0

		for {
			select {
			case <-ctx.Done():
				logger.Info("Context cancelled, stopping echo")
				echoDone <- nil
				return
			default:
				// Read RTP packet
				n, clientAddr, err := rtpConn.ReadFromUDP(buffer)
				if err != nil {
					logger.Error("Failed to read RTP packet", "error", err)
					echoDone <- err
					return
				}

				packetCount++
				if packetCount%10 == 0 {
					logger.Info("RTP packets received", "count", packetCount, "bytes", n, "from", clientAddr.String())
				}

				// Echo the packet using the separate send connection
				_, err = sendConn.Write(buffer[:n])
				if err != nil {
					logger.Error("Failed to echo RTP packet", "error", err)
					echoDone <- err
					return
				}

				if packetCount%10 == 0 {
					logger.Info("RTP packet echoed", "to", fmt.Sprintf("%s:%d", remoteIP, remotePort))
				}
			}
		}
	}()

	// Wait for context cancellation or echo completion
	select {
	case <-ctx.Done():
		logger.Info("Shutting down RTP echo server")
		return nil
	case err := <-echoDone:
		if err != nil {
			return fmt.Errorf("echo failed: %w", err)
		}
		logger.Info("Echo completed")
		return nil
	}
}
