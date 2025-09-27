package scenarios

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

func AnswerWithEcho(inDialog *diago.DialogServerSession, timeoutCtx context.Context) error {
	inDialog.Trying()
	inDialog.Ringing()
	if err := inDialog.Answer(); err != nil {
		return err
	}

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
		return nil
	}
	return err
}

func AnswerWithEchoWithValidation(inDialog *diago.DialogServerSession, timeoutCtx context.Context, expectByte uint8) error {
	inDialog.Trying()
	inDialog.Ringing()
	if err := inDialog.Answer(); err != nil {
		return err
	}

	if timeoutCtx != nil {
		select {
		case <-timeoutCtx.Done():
			slog.Info("Echo call timeout reached")
			return nil
		default:
		}
	}

	audioR, err := inDialog.Media().AudioReader()
	if err != nil {
		return err
	}
	audioW, err := inDialog.Media().AudioWriter()
	if err != nil {
		return err
	}

	codec := media.Codec{Name: "unknown"}
	if mediaSession := inDialog.Media().MediaSession(); mediaSession != nil {
		codec = media.CodecAudioFromSession(mediaSession)
	}

	if expectByte != 0 {
		audioR = NewRTPValidator(audioR, expectByte, codec)
	}

	_, err = media.Copy(audioR, audioW)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

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

func NewRTPValidator(reader io.Reader, expectByte uint8, codec media.Codec) *RTPValidator {
	return &RTPValidator{
		reader:     reader,
		expectByte: expectByte,
		startTime:  time.Now(),
		codec:      codec,
	}
}

func (v *RTPValidator) Read(p []byte) (int, error) {
	n, err := v.reader.Read(p)
	if err != nil {
		return n, err
	}

	if v.expectByte == 0 {
		return n, err
	}

	v.packetCount++

	if v.packetCount <= 15 {
		return n, err
	}

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

	windowDuration := time.Since(v.windowStart)
	packetsInWindow := v.packetCount - 15

	if windowDuration >= 3*time.Second || packetsInWindow >= 50 {
		if v.validCount == packetsInWindow && packetsInWindow >= 50 {
			slog.Info("RTP_ASSERT_OK",
				"codec", v.codec.Name,
				"bytes", fmt.Sprintf("0x%02X", v.expectByte),
				"packets", packetsInWindow,
				"duration", windowDuration,
				"last_raw", fmt.Sprintf("0x%02X", v.lastByte))
		} else {
			slog.Error("RTP_ASSERT_FAIL",
				"codec", v.codec.Name,
				"expected", fmt.Sprintf("0x%02X", v.expectByte),
				"valid_packets", v.validCount,
				"total_packets", packetsInWindow,
				"duration", windowDuration,
				"last_raw", fmt.Sprintf("0x%02X", v.lastByte))
		}

		v.validCount = 0
		v.windowStart = time.Now()
	}

	return n, err
}
