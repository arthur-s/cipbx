package scenarios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo/sip"
)

func BridgeCall(
	dg *diago.Diago,
	inDialog *diago.DialogServerSession,
	callee string,
	timeoutCtx context.Context,
	lookup func(string) (sip.Uri, bool),
) error {
	inDialog.Trying()
	inDialog.Ringing()

	var recipient sip.Uri
	if lookup != nil {
		if reg, ok := lookup(callee); ok {
			recipient = reg
		}
	}
	if recipient.User == "" {
		recipient = sip.Uri{
			User: callee,
			Host: inDialog.InviteRequest.To().Address.Host,
			Port: 5060,
		}
	}

	bridge := diago.NewBridge()

	ctx, cancel := context.WithTimeout(inDialog.Context(), 30*time.Second)
	defer cancel()

	outDialog, err := dg.InviteBridge(ctx, recipient, &bridge, diago.InviteOptions{})
	if err != nil {
		_ = inDialog.Respond(sip.StatusTemporarilyUnavailable, "Temporarily Unavailable", nil)
		return fmt.Errorf("failed to create bridged call: %w", err)
	}
	defer outDialog.Close()

	if err := inDialog.Answer(); err != nil {
		return err
	}
	if err := bridge.AddDialogSession(inDialog); err != nil {
		return fmt.Errorf("failed to add incoming dialog to bridge: %w", err)
	}

	slog.Info("Call bridged", "from", inDialog.ID, "to", outDialog.ID, "callee", callee)

	var timeoutDone <-chan struct{}
	if timeoutCtx != nil {
		timeoutDone = timeoutCtx.Done()
	}

	hangupWithLog := func(label string, dlg interface{ Hangup(context.Context) error }) {
		hangupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := dlg.Hangup(hangupCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("Failed to hang up dialog", "direction", label, "callee", callee, "error", err)
		}
	}

	select {
	case <-timeoutDone:
		slog.Info("Bridge call timeout reached", "callee", callee)
		hangupWithLog("outgoing", outDialog)
		hangupWithLog("incoming", inDialog)
	case <-inDialog.Context().Done():
		slog.Info("Incoming call hung up", "callee", callee)
		hangupWithLog("outgoing", outDialog)
	case <-outDialog.Context().Done():
		slog.Info("Outgoing call hung up", "callee", callee)
		hangupWithLog("incoming", inDialog)
	}

	return nil
}
