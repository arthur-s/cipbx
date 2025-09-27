package scenarios

import (
	"context"
	"log/slog"
	"os"

	"github.com/emiago/diago"
)

func AnswerWithPlayback(inDialog *diago.DialogServerSession, timeoutCtx context.Context) error {
	inDialog.Trying()
	inDialog.Ringing()
	if err := inDialog.Answer(); err != nil {
		return err
	}

	if timeoutCtx != nil {
		select {
		case <-timeoutCtx.Done():
			slog.Info("Playback call timeout reached")
			return nil
		default:
		}
	}

	pb, err := inDialog.PlaybackCreate()
	if err != nil {
		slog.Error("Failed to create playback", "error", err)
		return err
	}

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
