package runtime

import (
 "math/big"
 "os"
 "path/filepath"
 "testing"

 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

func f20Vote(set uint64,signer types.Address) ilnCheckpointVote{
 msg:=protocol.XITADirectMessage{
  SourceChainID:8453,SourceDomain:8453,DestinationDomain:1643,
  RouteID:types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
  SetID:set,SourceBlockNumber:100,
  SourceRegistry:types.StringToAddress("0x1111111111111111111111111111111111111111"),
  Gateway:types.StringToAddress("0x2222222222222222222222222222222222222222"),
  SourceRouter:types.StringToAddress("0x3333333333333333333333333333333333333333"),
  SourceMailbox:types.StringToAddress("0x4444444444444444444444444444444444444444"),
  DestinationRouter:types.StringToAddress("0x5555555555555555555555555555555555555555"),
  ValidatorFeeWei:big.NewInt(1000),
  MessageID:types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
  DestinationISM:types.StringToAddress("0x6666666666666666666666666666666666666666"),
  DestinationRegistry:types.StringToAddress("0x7777777777777777777777777777777777777777"),
 }
 raw,err:=msg.MarshalBinary();if err!=nil{panic(err)}
 return ilnCheckpointVote{Route:"base-xgr",Payload:raw,Signer:signer,Signature:[]byte{1,2,3}}
}
func TestF20DurableSuccessorRequiredBeforeOldVoteDeletion(t *testing.T){
 signer:=types.StringToAddress("0x1111111111111111111111111111111111111111")
 w:=&Worker{dataDir:t.TempDir(),localAddr:signer,
  ilnLocalVotes:make(map[types.Hash]*ilnLocalVoteState)}
 old:=f20Vote(7,signer)
 next:=f20Vote(8,signer)
 hash:=crypto.Keccak256Hash(next.Payload)
 // A successful signing helper returning nil without a stored successor
 // must never authorize deleting the historical vote.
 if w.hasDurableILNSuccessorVote(old,8){t.Fatal("missing successor counted as durable")}
 // Memory alone is not persistence.
 w.ilnLocalVotes[hash]=&ilnLocalVoteState{vote:next}
 if w.hasDurableILNSuccessorVote(old,8){t.Fatal("memory-only successor accepted")}
 if err:=w.persistILNLocalVote(hash,next);err!=nil{t.Fatal(err)}
 if !w.hasDurableILNSuccessorVote(old,8){t.Fatal("durably stored current-set vote not accepted")}
 if w.hasDurableILNSuccessorVote(old,9){t.Fatal("wrong validator set accepted")}
 // A different sender, source message, or corrupted journal must not let
 // the worker discard original recovery evidence.
 bad:=next
 bad.Signer=types.StringToAddress("0x2222222222222222222222222222222222222222")
 w.ilnLocalVotes[hash]=&ilnLocalVoteState{vote:bad}
 if w.hasDurableILNSuccessorVote(old,8){t.Fatal("different signer accepted")}
 w.ilnLocalVotes[hash]=&ilnLocalVoteState{vote:next}
 path:=filepath.Join(w.ilnVoteDir(),hash.String()+".json")
 if err:=os.WriteFile(path,[]byte("{}"),0o660);err!=nil{t.Fatal(err)}
 if w.hasDurableILNSuccessorVote(old,8){t.Fatal("corrupt persisted successor accepted")}
 if err:=w.persistILNLocalVote(hash,next);err!=nil{t.Fatal(err)}
 altered:=f20Vote(8,signer)
 altered.Route="other-route"
 w.ilnLocalVotes[hash]=&ilnLocalVoteState{vote:altered}
 if w.hasDurableILNSuccessorVote(old,8){t.Fatal("different route accepted")}
}
