package validators

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
)

type ValidatorRow struct {
	Address      string `json:"address"`
	StakeWei     string `json:"stakeWei"`
	BLSPubKeyHex string `json:"blsPubKeyHex,omitempty"`
}

type ValidatorsResult struct {
	MinValidatorCount     string         `json:"minValidatorCount"`
	MaxValidatorCount     string         `json:"maxValidatorCount"`
	ValidatorThresholdWei string         `json:"validatorThresholdWei"`
	Validators            []ValidatorRow `json:"validators"`
}

func (r *ValidatorsResult) GetOutput() string {
	var b bytes.Buffer

	b.WriteString("\n[IBFT VALIDATORS]\n")
	b.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Min validator count|%s", r.MinValidatorCount),
		fmt.Sprintf("Max validator count|%s", r.MaxValidatorCount),
		fmt.Sprintf("Validator threshold (wei)|%s", r.ValidatorThresholdWei),
	}))

	for i, v := range r.Validators {
		rows := []string{
			fmt.Sprintf("Validator %d address|%s", i, v.Address),
			fmt.Sprintf("Validator %d stake (wei)|%s", i, v.StakeWei),
		}
		if v.BLSPubKeyHex != "" {
			rows = append(rows, fmt.Sprintf("Validator %d BLS pubkey|%s", i, v.BLSPubKeyHex))
		}
		b.WriteString("\n")
		b.WriteString(helper.FormatKV(rows))
	}

	return b.String()
}
