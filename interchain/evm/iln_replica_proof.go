package evm

import (
 "fmt"
 "github.com/xgr-network/xgr-node/types"
)

// BuildILNReplicaProof produces a standard 32-sibling Hyperlane Merkle proof
// from a fully validated local validator replica. It does not require a
// company relayer, remote historical eth_getLogs or a private signing key.
func BuildILNReplicaProof(replica ILNReplica, canonical ILNReplicaCheckpoint, index uint32) ([ilnMerkleDepth]types.Hash,error) {
 var proof [ilnMerkleDepth]types.Hash
 if err:=VerifyILNReplica(replica,canonical);err!=nil{return proof,err}
 if uint64(index)>=canonical.Count {return proof,fmt.Errorf("ILN replica proof index outside checkpoint")}
 zeros:=[ilnMerkleDepth]types.Hash{}
 zeros[0]=types.ZeroHash
 for i:=1;i<ilnMerkleDepth;i++{zeros[i]=ilnMerkleParent(zeros[i-1],zeros[i-1])}
 type node struct{level uint8;start uint64}
 cache:=make(map[node]types.Hash)
 var rootOf func(uint8,uint64)types.Hash
 rootOf=func(level uint8,start uint64)types.Hash{
  if start>=canonical.Count{return zeros[level]}
  if level==0{return replica.Leaves[start]}
  k:=node{level,start}
  if v,ok:=cache[k];ok{return v}
  half:=uint64(1)<<(level-1)
  v:=ilnMerkleParent(rootOf(level-1,start),rootOf(level-1,start+half))
  cache[k]=v
  return v
 }
 for level:=0;level<ilnMerkleDepth;level++{
  size:=uint64(1)<<level
  siblingStart:=(uint64(index)^size)&^(size-1)
  proof[level]=rootOf(uint8(level),siblingStart)
 }
 current:=replica.Leaves[index]
 for level:=0;level<ilnMerkleDepth;level++{
  if (uint64(index)>>level)&1==1{current=ilnMerkleParent(proof[level],current)}else{current=ilnMerkleParent(current,proof[level])}
 }
 if current!=canonical.Root{return [ilnMerkleDepth]types.Hash{},ErrILNReplicaUnverified}
 return proof,nil
}
