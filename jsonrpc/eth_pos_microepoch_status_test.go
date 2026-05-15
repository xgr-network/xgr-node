package jsonrpc

import "testing"

func TestReadMicroEpochConfigFromChain_StringValues(t *testing.T) {
	engine := map[string]interface{}{
		"ibft": map[string]interface{}{
			"config": map[string]interface{}{
				"microEpochSize":               "0x8",
				"microEpochNominalWeightUnits": "10000",
				"microEpochInactivityDecayBps": "9000",
			},
		},
	}
	cfg := readMicroEpochConfigFromChain(engine)
	if cfg.microEpochSize != 8 || cfg.nominalWeightUnits != 10000 || cfg.inactivityDecayBps != 9000 {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}
