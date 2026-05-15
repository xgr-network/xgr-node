package snapshot

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
	ibftHelper "github.com/xgr-network/xgr-node/command/ibft/helper"
	ibftOp "github.com/xgr-network/xgr-node/consensus/ibft/proto"
	"github.com/xgr-network/xgr-node/validators"
)

type IBFTSnapshotVote struct {
	Proposer string          `json:"proposer"`
	Address  string          `json:"address"`
	Vote     ibftHelper.Vote `json:"vote"`
}

type IBFTSnapshotValidator struct {
	Validator             validators.Validator `json:"validator"`
	UptimeNominalWeight   uint64               `json:"uptime_nominal_weight"`
	UptimeEffectiveWeight uint64               `json:"uptime_effective_weight"`
	UptimeInactivity      uint64               `json:"uptime_inactivity"`
}

type IBFTSnapshotResult struct {
	Number                     uint64                  `json:"number"`
	Hash                       string                  `json:"hash"`
	MicroEpoch                 uint64                  `json:"micro_epoch"`
	UptimeEpoch                uint64                  `json:"uptime_epoch"`
	UptimeTotalEffectiveWeight uint64                  `json:"uptime_total_effective_weight"`
	UptimeActiveEffective      uint64                  `json:"uptime_active_effective_weight"`
	Votes                      []IBFTSnapshotVote      `json:"votes"`
	Validators                 []IBFTSnapshotValidator `json:"validators"`
}

func newIBFTSnapshotResult(resp *ibftOp.Snapshot) (*IBFTSnapshotResult, error) {
	res := &IBFTSnapshotResult{
		Number:                     resp.Number,
		Hash:                       resp.Hash,
		MicroEpoch:                 resp.MicroEpoch,
		UptimeEpoch:                resp.UptimeEpoch,
		UptimeTotalEffectiveWeight: resp.UptimeTotalEffectiveWeight,
		UptimeActiveEffective:      resp.UptimeActiveEffectiveWeight,
		Votes:                      make([]IBFTSnapshotVote, len(resp.Votes)),
		Validators:                 make([]IBFTSnapshotValidator, len(resp.Validators)),
	}

	for i, v := range resp.Votes {
		res.Votes[i].Proposer = v.Validator
		res.Votes[i].Address = v.Proposed
		res.Votes[i].Vote = ibftHelper.BoolToVote(v.Auth)
	}

	var (
		validatorType validators.ValidatorType
		err           error
	)

	for i, v := range resp.Validators {
		if validatorType, err = validators.ParseValidatorType(v.Type); err != nil {
			return nil, err
		}

		validator, err := validators.NewValidatorFromType(validatorType)
		if err != nil {
			return nil, err
		}

		if err := validator.SetFromBytes(v.Data); err != nil {
			return nil, err
		}

		res.Validators[i] = IBFTSnapshotValidator{
			Validator:             validator,
			UptimeNominalWeight:   v.UptimeNominalWeight,
			UptimeEffectiveWeight: v.UptimeEffectiveWeight,
			UptimeInactivity:      v.UptimeInactivity,
		}
	}

	return res, nil
}

func (r *IBFTSnapshotResult) GetOutput() string {
	var buffer bytes.Buffer

	buffer.WriteString("\n[IBFT SNAPSHOT]\n")
	r.writeBlockData(&buffer)
	r.writeVoteData(&buffer)
	r.writeValidatorData(&buffer)

	return buffer.String()
}

func (r *IBFTSnapshotResult) writeBlockData(buffer *bytes.Buffer) {
	lines := []string{
		fmt.Sprintf("Block|%d", r.Number),
		fmt.Sprintf("Hash|%s", r.Hash),
	}
	if r.uptimeFieldsVisible() {
		lines = append(lines,
			fmt.Sprintf("Micro-epoch|%d", r.MicroEpoch),
			fmt.Sprintf("Uptime epoch|%d", r.UptimeEpoch),
			fmt.Sprintf("Uptime total effective weight|%d", r.UptimeTotalEffectiveWeight),
			fmt.Sprintf("Uptime active effective weight|%d", r.UptimeActiveEffective),
		)
	}
	buffer.WriteString(helper.FormatKV(lines))
	buffer.WriteString("\n")
}

func (r *IBFTSnapshotResult) writeVoteData(buffer *bytes.Buffer) {
	numVotes := len(r.Votes)
	votes := make([]string, numVotes+1)

	votes[0] = "No votes found"

	if numVotes > 0 {
		votes[0] = "PROPOSER|ADDRESS|VOTE TO ADD"

		for i, d := range r.Votes {
			votes[i+1] = fmt.Sprintf(
				"%s|%s|%s",
				d.Proposer,
				d.Address,
				ibftHelper.VoteToString(d.Vote),
			)
		}
	}

	buffer.WriteString("\n[VOTES]\n")
	buffer.WriteString(helper.FormatList(votes))
	buffer.WriteString("\n")
}

func (r *IBFTSnapshotResult) writeValidatorData(buffer *bytes.Buffer) {
	numValidators := len(r.Validators)
	validators := make([]string, numValidators+1)
	validators[0] = "No validators found"

	if numValidators > 0 {
		if r.uptimeFieldsVisible() {
			validators[0] = "ADDRESS|UPTIME NOMINAL|UPTIME EFFECTIVE|UPTIME INACTIVITY"
		} else {
			validators[0] = "ADDRESS"
		}
		for i, d := range r.Validators {
			if r.uptimeFieldsVisible() {
				validators[i+1] = fmt.Sprintf("%s|%d|%d|%d", d.Validator.String(), d.UptimeNominalWeight, d.UptimeEffectiveWeight, d.UptimeInactivity)
			} else {
				validators[i+1] = d.Validator.String()
			}
		}
	}

	buffer.WriteString("\n[VALIDATORS]\n")
	buffer.WriteString(helper.FormatList(validators))
	buffer.WriteString("\n")
}

func (r *IBFTSnapshotResult) uptimeFieldsVisible() bool {
	if r.MicroEpoch != 0 ||
		r.UptimeEpoch != 0 ||
		r.UptimeTotalEffectiveWeight != 0 ||
		r.UptimeActiveEffective != 0 {
		return true
	}

	for _, v := range r.Validators {
		if v.UptimeNominalWeight != 0 ||
			v.UptimeEffectiveWeight != 0 ||
			v.UptimeInactivity != 0 {
			return true
		}
	}

	return false
}
