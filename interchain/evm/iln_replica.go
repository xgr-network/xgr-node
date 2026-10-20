package evm

import (
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "path/filepath"

 "github.com/xgr-network/xgr-node/types"
)

// ILNReplicaCheckpoint MUST originate from a confirmed Source MerkleTreeHook
// read on an independent RPC; a peer-supplied root is never an authority.
type ILNReplicaCheckpoint struct {
 ChainID uint64
 Hook types.Address
 Root types.Hash
 Count uint64
 BlockNumber uint64
}

// ILNReplicaChunk is an untrusted transport envelope. A peer may deliver it
// over any network; transport authentication is not sufficient for acceptance.
type ILNReplicaChunk struct {
 ChainID uint64
 Hook types.Address
 Offset uint64
 Leaves []types.Hash
}

// ILNReplica is a validated local replica of a prefix of a Hyperlane tree.
// It contains NO signing keys and cannot mint tokens or authorize transfers.
type ILNReplica struct {
 ChainID uint64 `json:"chainId"`
 Hook types.Address `json:"hook"`
 CheckpointBlock uint64 `json:"checkpointBlock"`
 Root types.Hash `json:"root"`
 Leaves []types.Hash `json:"leaves"`
}

var ErrILNReplicaUnverified = errors.New("ILN replica cannot be verified against canonical source checkpoint")

// VerifyILNReplica rebuilds the COMPLETE Merkle prefix independently. A
// corrupt or missing insertion, a wrong order or a poisoned peer response can
// never be made canonical by its peer's assertion or local checksum.
func VerifyILNReplica(replica ILNReplica, canonical ILNReplicaCheckpoint) error {
 if canonical.ChainID==0 || canonical.Hook==types.ZeroAddress ||
  canonical.Root==types.ZeroHash || canonical.Count==0 ||
  canonical.BlockNumber==0 || canonical.Count>1<<20 ||
  replica.ChainID!=canonical.ChainID || replica.Hook!=canonical.Hook ||
  replica.CheckpointBlock!=canonical.BlockNumber ||
  replica.Root!=canonical.Root || uint64(len(replica.Leaves))!=canonical.Count {
  return ErrILNReplicaUnverified
 }
 tree:=&ilnMerkleAccumulator{}
 for i,id:=range replica.Leaves {
  if err:=tree.insert(uint32(i),id);err!=nil{return fmt.Errorf("%w: insertion %d: %v",ErrILNReplicaUnverified,i,err)}
 }
 if tree.root()!=canonical.Root {return fmt.Errorf("%w: reconstructed root mismatch",ErrILNReplicaUnverified)}
 return nil
}

// AssembleILNReplica strictly joins bounded, potentially adversarial chunks.
// No candidate is persisted and no BLS signing is authorized until the final
// complete tree has matched the independently read on-chain checkpoint.
func AssembleILNReplica(canonical ILNReplicaCheckpoint,chunks []ILNReplicaChunk)(ILNReplica,error) {
 replica:=ILNReplica{ChainID:canonical.ChainID,Hook:canonical.Hook,CheckpointBlock:canonical.BlockNumber,Root:canonical.Root}
 if canonical.Count==0 || canonical.Count>1<<20 || len(chunks)==0 {return ILNReplica{},ErrILNReplicaUnverified}
 for _,chunk:=range chunks {
  if chunk.ChainID!=canonical.ChainID || chunk.Hook!=canonical.Hook ||
   chunk.Offset!=uint64(len(replica.Leaves)) || len(chunk.Leaves)==0 ||
   len(chunk.Leaves)>1024 || uint64(len(replica.Leaves)+len(chunk.Leaves))>canonical.Count {
   return ILNReplica{},ErrILNReplicaUnverified
  }
  replica.Leaves=append(replica.Leaves,chunk.Leaves...)
 }
 if err:=VerifyILNReplica(replica,canonical);err!=nil{return ILNReplica{},err}
 return replica,nil
}

// StoreVerifiedILNReplica never overwrites a validated live snapshot with
// data that has not been independently verified. Atomic rename prevents a
// process interruption from installing a half-written candidate.
func StoreVerifiedILNReplica(path string,replica ILNReplica,canonical ILNReplicaCheckpoint)error{
 if err:=VerifyILNReplica(replica,canonical);err!=nil{return err}
 if path==""{return fmt.Errorf("empty replica path")}
 if err:=os.MkdirAll(filepath.Dir(path),0o770);err!=nil{return err}
 data,err:=json.Marshal(replica);if err!=nil{return err}
 f,err:=os.CreateTemp(filepath.Dir(path),".iln-replica-*");if err!=nil{return err}
 defer os.Remove(f.Name())
 if err=f.Chmod(0o660);err!=nil{f.Close();return err}
 if _,err=f.Write(data);err!=nil{f.Close();return err}
 if err=f.Sync();err!=nil{f.Close();return err}
 if err=f.Close();err!=nil{return err}
 return os.Rename(f.Name(),path)
}

func LoadVerifiedILNReplica(path string,canonical ILNReplicaCheckpoint)(ILNReplica,error){
 raw,err:=os.ReadFile(path);if err!=nil{return ILNReplica{},err}
 if len(raw)>40<<20{return ILNReplica{},ErrILNReplicaUnverified}
 var r ILNReplica
 if err:=json.Unmarshal(raw,&r);err!=nil{return ILNReplica{},err}
 if err:=VerifyILNReplica(r,canonical);err!=nil{return ILNReplica{},err}
 return r,nil
}
