package evm

import (
 "fmt"
 "strings"

 "github.com/xgr-network/xgr-node/types"
)

// CheckpointRoute is the historical Go symbol for a Direct-BLS route.
// It contains no checkpoint/Merkle state; routes originate solely in the
// permissionless, receipt-verified ILN source Registry.
type CheckpointRoute struct {
 Name string
 SourceNetwork string
 Destination string
 RouteID types.Hash
}
func (r *CheckpointRoute) Validate() error {
 if r==nil||strings.TrimSpace(r.Name)==""||strings.TrimSpace(r.SourceNetwork)==""||
  strings.TrimSpace(r.Destination)==""||r.SourceNetwork==r.Destination||
  r.RouteID==types.ZeroHash{
  return fmt.Errorf("invalid Direct-BLS route identity")
 }
 return nil
}
