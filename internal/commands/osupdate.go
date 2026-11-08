package commands

import (
	"context"
	"encoding/json"
	"time"
)

// osUpdateTimeout covers a large Windows cumulative update.
const osUpdateTimeout = 4 * time.Hour

// rebootAfterUpdate is the delay before the reboot an update asked for.
const rebootAfterUpdate = time.Minute

type osUpdateParams struct {
	Reboot bool `json:"reboot"`
}

func osUpdate(ctx context.Context, params json.RawMessage, out *Output) error {
	var p osUpdateParams
	if err := decode(params, &p); err != nil {
		return err
	}
	needReboot, err := installUpdates(ctx, out)
	if err != nil {
		return err
	}
	switch {
	case needReboot && p.Reboot:
		return scheduleReboot(ctx, rebootAfterUpdate, "Restarting to finish installing updates.", out)
	case needReboot:
		out.Printf("A reboot is needed to finish installing updates.")
	}
	return nil
}
