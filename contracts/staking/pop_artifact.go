package staking

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

//go:embed pop-artifact/StakingV2PoP.json
var popArtifactJSON []byte

// PoPBytecodes returns independent copies of the pinned legacy and candidate
// runtime code. The legacy bootstrap constant is intentionally not replaced.
func PoPBytecodes() (legacy, candidate []byte, err error) {
	var artifact struct {
		Compiler string `json:"compiler"`
		Runtime  string `json:"runtimeBytecode"`
		Legacy   string `json:"legacyRuntimeBytecode"`
		SHA256   string `json:"runtimeSha256"`
	}
	if err = json.Unmarshal(popArtifactJSON, &artifact); err != nil {
		return nil, nil, err
	}
	if artifact.Compiler != "0.8.30+commit.73712a01.Emscripten.clang" || len(artifact.Runtime) < 3 || len(artifact.Legacy) < 3 {
		return nil, nil, fmt.Errorf("invalid PoP bytecode artifact")
	}
	candidate, err = hex.DecodeString(artifact.Runtime[2:])
	if err != nil {
		return nil, nil, err
	}
	legacy, err = hex.DecodeString(artifact.Legacy[2:])
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(candidate)
	if hex.EncodeToString(sum[:]) != artifact.SHA256 {
		return nil, nil, fmt.Errorf("PoP runtime checksum mismatch")
	}
	return legacy, candidate, nil
}
