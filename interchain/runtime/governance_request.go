package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
)

type GovernanceRequestResult struct {
	Done       bool
	Error      string
	ProposalID string
	Approved   bool
	Quorum     bool
}

func EnqueueGovernanceCreateAndWait(
	dataDir string,
	proposal protocol.ILNGovernanceProposal,
	timeout time.Duration,
) (*GovernanceRequestResult, error) {
	raw, err := proposal.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return enqueueGovernanceAndWait(dataDir, localGovernanceRequest{
		Action:  "create",
		Payload: raw,
	}, timeout)
}

func EnqueueGovernanceApproveAndWait(
	dataDir string,
	proposalID string,
	timeout time.Duration,
) (*GovernanceRequestResult, error) {
	if _, err := parseGovernanceProposalID(proposalID); err != nil {
		return nil, err
	}
	return enqueueGovernanceAndWait(dataDir, localGovernanceRequest{
		Action:     "approve",
		ProposalID: proposalID,
	}, timeout)
}

func enqueueGovernanceAndWait(
	dataDir string,
	req localGovernanceRequest,
	timeout time.Duration,
) (*GovernanceRequestResult, error) {
	if stringsTrim(dataDir) == "" {
		return nil, fmt.Errorf("node data dir is required")
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	heartbeat := filepath.Join(dataDir, "interchain", "worker.heartbeat")
	info, err := os.Stat(heartbeat)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("interchain worker is not running for data dir %s", dataDir)
		}
		return nil, err
	}
	if time.Since(info.ModTime()) > 10*time.Second {
		return nil, fmt.Errorf("interchain worker heartbeat is stale for data dir %s", dataDir)
	}

	requestDir := filepath.Join(dataDir, "interchain", "governance", "requests")
	resultDir := filepath.Join(dataDir, "interchain", "governance", "results")
	if err := os.MkdirAll(requestDir, 0o770); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(resultDir, 0o770); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(requestDir, id+".json.tmp")
	path := filepath.Join(requestDir, id+".json")
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}

	resultPath := filepath.Join(resultDir, id+".json")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rawResult, err := os.ReadFile(resultPath)
		if err == nil {
			var result localGovernanceResult
			if err := json.Unmarshal(rawResult, &result); err != nil {
				return nil, err
			}
			_ = os.Remove(resultPath)
			if result.Error != "" {
				return nil, fmt.Errorf("%s", result.Error)
			}
			return &GovernanceRequestResult{
				Done:       result.Done,
				Error:      result.Error,
				ProposalID: result.ProposalID,
				Approved:   result.Approved,
				Quorum:     result.Quorum,
			}, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		time.Sleep(250 * time.Millisecond)
	}

	return nil, fmt.Errorf("timed out waiting for ILN governance worker result")
}
