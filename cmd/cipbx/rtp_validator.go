package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
)

// RTPValidator wraps an audio reader to validate RTP payload bytes
type RTPValidator struct {
	reader      io.Reader
	expectByte  uint8
	packetCount int
	validCount  int
	startTime   time.Time
	windowStart time.Time
	codec       media.Codec
	lastByte    uint8
	debugCount  int
}

// NewRTPValidator creates a new RTP validator
func NewRTPValidator(reader io.Reader, expectByte uint8, codec media.Codec) *RTPValidator {
	return &RTPValidator{
		reader:     reader,
		expectByte: expectByte,
		startTime:  time.Now(),
		codec:      codec,
	}
}

// Read implements io.Reader interface
func (v *RTPValidator) Read(p []byte) (int, error) {
	n, err := v.reader.Read(p)
	if err != nil {
		return n, err
	}

	// Skip validation if expectByte is 0 (disabled)
	if v.expectByte == 0 {
		return n, err
	}

	// Validate payload bytes
	v.packetCount++

	// Ignore first 15 packets (startup/comfort noise)
	if v.packetCount <= 15 {
		return n, err
	}

	// Start validation window after 15 packets
	if v.packetCount == 16 {
		v.windowStart = time.Now()
	}

	if n > 0 {
		v.lastByte = p[n-1]
	}

	allMatch := true
	for _, b := range p[:n] {
		if b != v.expectByte {
			allMatch = false
			break
		}
	}

	// Debug logging every 25th packet (approximately every 0.5 second)
	v.debugCount++
	if v.debugCount%25 == 0 {
		slog.Debug("RTP validation debug",
			"packet", v.packetCount,
			"codec", v.codec.Name,
			"expected", fmt.Sprintf("0x%02X", v.expectByte),
			"last_raw", fmt.Sprintf("0x%02X", v.lastByte),
			"match", allMatch)
	}

	if allMatch {
		v.validCount++
	}

	// Check validation window (3 seconds or 50+ packets)
	windowDuration := time.Since(v.windowStart)
	packetsInWindow := v.packetCount - 15

	if windowDuration >= 3*time.Second || packetsInWindow >= 50 {
		// Validation window complete
		if v.validCount == packetsInWindow && packetsInWindow >= 50 {
			// All packets in window were valid
			slog.Info("RTP_ASSERT_OK",
				"codec", v.codec.Name,
				"bytes", fmt.Sprintf("0x%02X", v.expectByte),
				"packets", packetsInWindow,
				"duration", windowDuration,
				"last_raw", fmt.Sprintf("0x%02X", v.lastByte))
		} else {
			// Some packets were invalid
			slog.Error("RTP_ASSERT_FAIL",
				"codec", v.codec.Name,
				"expected", fmt.Sprintf("0x%02X", v.expectByte),
				"valid_packets", v.validCount,
				"total_packets", packetsInWindow,
				"duration", windowDuration,
				"last_raw", fmt.Sprintf("0x%02X", v.lastByte))
		}

		// Reset for next window (if any)
		v.validCount = 0
		v.windowStart = time.Now()
	}

	return n, err
}

// AnswerWithEchoWithValidation performs echo with RTP payload validation
func AnswerWithEchoWithValidation(inDialog *diago.DialogServerSession, timeoutCtx context.Context, expectByte uint8) error {
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

	// Get audio reader and writer
	audioR, err := inDialog.Media().AudioReader()
	if err != nil {
		return err
	}
	audioW, err := inDialog.Media().AudioWriter()
	if err != nil {
		return err
	}

	// Determine codec for logging
	codec := media.Codec{Name: "unknown"}
	if mediaSession := inDialog.Media().MediaSession(); mediaSession != nil {
		codec = media.CodecAudioFromSession(mediaSession)
	}

	// Wrap audio reader with validator if expectByte is specified
	if expectByte != 0 {
		audioR = NewRTPValidator(audioR, expectByte, codec)
	}

	// Perform echo with validation
	_, err = media.Copy(audioR, audioW)
	if errors.Is(err, io.EOF) {
		// Call finished
		return nil
	}
	return err
}
