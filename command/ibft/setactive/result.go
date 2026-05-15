package setactive

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
)

type SetActiveResult struct {
	Validator               string `json:"validator"`
	JoinedAtBlock           uint64 `json:"joinedAtBlock,omitempty"`
	Active                  bool   `json:"active"`
	AccountStakeWei         string `json:"accountStakeWei"`
	DeactivatedAtBlock      uint64 `json:"deactivatedAtBlock,omitempty"`
	UnstakeAvailableAtBlock uint64 `json:"unstakeAvailableAtBlock,omitempty"`
}

func (r *SetActiveResult) GetOutput() string {
	var b bytes.Buffer
	b.WriteString("\n[IBFT SET-ACTIVE]\n")
	rows := []string{
		fmt.Sprintf("Validator|%s", r.Validator),
		fmt.Sprintf("Joined at block|%d", r.JoinedAtBlock),
		fmt.Sprintf("Active|%t", r.Active),
		fmt.Sprintf("Account stake (wei)|%s", r.AccountStakeWei),
	}

	if r.DeactivatedAtBlock > 0 {
		rows = append(rows, fmt.Sprintf("Deactivated at block|%d", r.DeactivatedAtBlock))
	}
	if r.UnstakeAvailableAtBlock > 0 {
		rows = append(rows, fmt.Sprintf("Unstake available at block|%d", r.UnstakeAvailableAtBlock))
	}

	b.WriteString(helper.FormatKV(rows))

	if !r.Active && r.UnstakeAvailableAtBlock > 0 {
		b.WriteString("\nNote: Unstake is available from the displayed block onward (next epoch).\n")
	}
	if r.Active && r.DeactivatedAtBlock == 0 {
		b.WriteString("\nNote: deactivatedAtBlock=0 means 'never deactivated'. For withdraw/unstake, run set-active false first and wait for the next epoch.\n")
	}

	return b.String()
}
