package runtime

import (
 "encoding/hex"
 "encoding/json"
 "fmt"
 "math/big"
 "os"
 "path/filepath"
 "strings"

 "github.com/xgr-network/xgr-node/crypto"
 evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
)

type governanceExecuteRequest struct {
 id string
 proposalID string
}

// Decoupled from the consensus-facing worker ticker. One bounded executor
// serializes this node's local governance submissions and never trusts RPC
// callers to supply a proposal, bitmap, or signature.
func (w *Worker) governanceExecuteLoop() {
 defer w.wg.Done()
 for {
  select {
  case job:=<-w.governanceExecuteCh:
   result:=w.executeGovernanceJob(job.proposalID)
   // An unrecorded result MUST NOT make us discard the durable request.
   if err:=w.writeLocalGovernanceResult(job.id,result);err!=nil {
    w.logger.Error("persist ILN governance execution result failed; retained request for safe retry","id",job.id,"err",err)
   }else {
    if err:=os.Remove(filepath.Join(w.governanceRequestDir(),job.id+".json"));err!=nil&&!os.IsNotExist(err){
     w.logger.Warn("remove completed ILN governance execute request","id",job.id,"err",err)
    }
   }
   w.mu.Lock()
   delete(w.governanceExecuteInFlight,job.id)
   w.mu.Unlock()
  case <-w.closeCh:
   return
  }
 }
}

var submitILNGovernance = evmInterchain.SubmitILNGovernance

func (w *Worker) executeGovernanceJob(proposalID string) localGovernanceResult {
 result:=localGovernanceResult{Done:true}
 id,err:=parseGovernanceProposalID(proposalID)
 if err!=nil {result.Error=err.Error();return result}
 idString:=id.String()
 quorumPath:=filepath.Join(w.governanceQuorumDir(),idString+".json")
 quorumRaw,err:=os.ReadFile(quorumPath)
 if err!=nil {result.Error=fmt.Sprintf("ILN governance quorum not ready: %v",err);return result}
 var quorum governanceQuorum
 if json.Unmarshal(quorumRaw,&quorum)!=nil || quorum.ProposalID!=idString{
  result.Error="invalid stored ILN governance quorum identity";return result
 }
 proposalPath:=filepath.Join(w.governanceProposalDir(),idString+".json")
 stored,proposal,payload,err:=readStoredGovernanceProposal(proposalPath)
 if err!=nil || stored.ProposalID!=idString || crypto.Keccak256Hash(payload)!=id {
  result.Error="ILN governance proposal missing or invalid";return result
 }
 if quorum.Payload!="0x"+hex.EncodeToString(payload)||
   quorum.SetID!=proposal.SetID||quorum.Nonce!=proposal.Nonce||
   quorum.ValidUntil!=proposal.ValidUntil||
   quorum.Registry!=proposal.Registry.String()||
   quorum.ProposalType!=uint8(proposal.Type)||
   quorum.SourceChainID!=proposal.Route.Key.SourceChainID||
   quorum.SourceDomain!=proposal.Route.Key.SourceDomain||
   quorum.DestinationDomain!=proposal.Route.Key.DestinationDomain||
   quorum.RouteID!=proposal.Route.Key.RouteID.String()||
   quorum.ValidatorFeeWei!=governanceFeeString(proposal) {
  result.Error="stored ILN governance quorum conflicts with signed canonical proposal";return result
 }
 source,err:=w.governanceSource(proposal)
 if err!=nil {result.Error=err.Error();return result}
 bitmapRaw,bitmapErr:=hex.DecodeString(strings.TrimPrefix(quorum.SignerBitmap,"0x"))
 signatureRaw,sigErr:=hex.DecodeString(strings.TrimPrefix(quorum.AggregateSignatureCompressed,"0x"))
 if bitmapErr!=nil||sigErr!=nil||len(bitmapRaw)==0||len(signatureRaw)==0 {
  result.Error="ILN governance bitmap/signature decoding failed";return result
 }
 receipt,err:=submitILNGovernance(source,w.txKey,proposal,new(big.Int).SetBytes(bitmapRaw),signatureRaw)
 if err!=nil {result.Error=err.Error();return result}
 result.ProposalID=idString
 result.Quorum=true
 result.TxHash=receipt.TxHash
 result.Nonce=receipt.Nonce
 return result
}
