package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type RequestResult struct {
	Done      bool
	Error     string
	Validator string
	Active    bool
	Changed   bool
	TxHash    string
	SetID     uint64
}

func EnqueueAndWait(dataDir, chain string, active bool, timeout time.Duration) (*RequestResult, error) {
	if stringsTrim(dataDir) == "" {
		return nil, fmt.Errorf("node data dir is required")
	}
	if stringsTrim(chain) == "" {
		return nil, fmt.Errorf("destination chain is required")
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

	requestDir := filepath.Join(dataDir, "interchain", "requests")
	resultDir := filepath.Join(dataDir, "interchain", "results")
	if err := os.MkdirAll(requestDir, 0o770); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(resultDir, 0o770); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	raw, err := json.Marshal(localRequest{Chain: chain, Active: active})
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(requestDir, id+".json.tmp")
	reqPath := filepath.Join(requestDir, id+".json")
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, reqPath); err != nil {
		return nil, err
	}

	resultPath := filepath.Join(resultDir, id+".json")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rawResult, err := os.ReadFile(resultPath)
		if err == nil {
			var result localResult
			if err := json.Unmarshal(rawResult, &result); err != nil {
				return nil, err
			}
			_ = os.Remove(resultPath)
			if result.Error != "" {
				return nil, fmt.Errorf("%s", result.Error)
			}
			return &RequestResult{
				Done: result.Done, Error: result.Error, Validator: result.Validator,
				Active: result.Active, Changed: result.Changed, TxHash: result.TxHash, SetID: result.SetID,
			}, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		time.Sleep(250 * time.Millisecond)
	}

	return nil, fmt.Errorf("timed out waiting for interchain worker result")
}