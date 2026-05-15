package withdraw

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
)

type WithdrawResult struct {
	Validator       string `json:"validator"`
	AmountWei       string `json:"amountWei"`
	AccountStakeWei string `json:"accountStakeWei"`
}

func (r *WithdrawResult) GetOutput() string {
	var b bytes.Buffer
	b.WriteString("\n[IBFT WITHDRAW]\n")
	b.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Validator|%s", r.Validator),
		fmt.Sprintf("Amount (wei)|%s", r.AmountWei),
		fmt.Sprintf("Account stake (wei)|%s", r.AccountStakeWei),
	}))

	return b.String()
}
