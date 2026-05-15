package unstake

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
)

type UnstakeResult struct {
	Validator       string `json:"validator"`
	AccountStakeWei string `json:"accountStakeWei"`
}

func (r *UnstakeResult) GetOutput() string {
	var b bytes.Buffer
	b.WriteString("\n[IBFT UNSTAKE]\n")
	b.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Validator|%s", r.Validator),
		fmt.Sprintf("Account stake (wei)|%s", r.AccountStakeWei),
	}))

	return b.String()
}
