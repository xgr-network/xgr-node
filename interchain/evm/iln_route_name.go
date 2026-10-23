package evm

import (
    "fmt"
    "strconv"
    "strings"

    "github.com/xgr-network/xgr-node/types"
)

// ParseILNRouteName accepts only the deterministic, permissionless ILN
// identity. A public quorum request may suggest this identity, but neither
// parsing nor persistence grants a signing or route-activation permission.
func ParseILNRouteName(name string) (uint32, uint32, types.Hash, error) {
    parts := strings.Split(name, "_")
    if len(parts) != 4 || parts[0] != "iln" || len(parts[3]) != 64 {
        return 0, 0, types.ZeroHash, fmt.Errorf("invalid canonical ILN route name")
    }
    source, err := strconv.ParseUint(parts[1], 10, 32)
    if err != nil || source == 0 {
        return 0, 0, types.ZeroHash, fmt.Errorf("invalid ILN source domain")
    }
    dest, err := strconv.ParseUint(parts[2], 10, 32)
    if err != nil || dest == 0 {
        return 0, 0, types.ZeroHash, fmt.Errorf("invalid ILN destination domain")
    }
    for _, c := range parts[3] {
        if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
            return 0, 0, types.ZeroHash, fmt.Errorf("invalid ILN route id")
        }
    }
    routeID := types.StringToHash("0x" + parts[3])
    if routeID == types.ZeroHash ||
        fmt.Sprintf("iln_%d_%d_%s", source, dest, parts[3]) != name {
        return 0, 0, types.ZeroHash, fmt.Errorf("noncanonical ILN route name")
    }
    return uint32(source), uint32(dest), routeID, nil
}
