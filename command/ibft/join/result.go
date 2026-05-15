package join

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
)

type JoinResult struct {
	Validator       string `json:"validator"`
	JoinedAtBlock   uint64 `json:"joinedAtBlock,omitempty"`
	BLSRegistered   bool   `json:"blsRegistered"`
	StakeWei        string `json:"stakeWei"`
	AccountStakeWei string `json:"accountStakeWei"`
	Eligible        bool   `json:"eligible"`
	Note            string `json:"note"`
}

func (r *JoinResult) GetOutput() string {
	var b bytes.Buffer
	b.WriteString("\n[IBFT JOIN]\n")
	b.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Validator|%s", r.Validator),
		fmt.Sprintf("Joined at block|%d", r.JoinedAtBlock),
		fmt.Sprintf("BLS registered|%t", r.BLSRegistered),
		fmt.Sprintf("Required stake (wei)|%s", r.StakeWei),
		fmt.Sprintf("Account stake (wei)|%s", r.AccountStakeWei),
		fmt.Sprintf("Eligible|%t", r.Eligible),
		fmt.Sprintf("Note|%s", r.Note),
	}))
	return b.String()
}
