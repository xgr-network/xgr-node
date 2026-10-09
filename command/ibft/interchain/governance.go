package interchain

import (
 "fmt"
 "strings"
 "time"
 "math/big"
 "encoding/hex"

 "github.com/spf13/cobra"
 "github.com/xgr-network/xgr-node/command"
 "github.com/xgr-network/xgr-node/command/helper"
 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 evmInterchain "github.com/xgr-network/xgr-node/interchain/evm"
 interchainRuntime "github.com/xgr-network/xgr-node/interchain/runtime"
 "github.com/xgr-network/xgr-node/types"
)

type feeCreateParams struct {
 dataDir, source, feeWei string
 nonce uint64
 ttl,timeout time.Duration
}
type feeApproveParams struct{ dataDir,proposalID string;timeout time.Duration }

type FeeResult struct {ProposalID string `json:"proposalId"`;Approved bool `json:"approved"`;Quorum bool `json:"quorum"`}
func (r *FeeResult) GetOutput()string{return "\n[IBFT INTERCHAIN SOURCE FEE]\n"+helper.FormatKV([]string{
 fmt.Sprintf("Proposal ID|%s",r.ProposalID),fmt.Sprintf("Approved|%t",r.Approved),fmt.Sprintf("Quorum complete|%t",r.Quorum),
})}

var (
 enqueueGovernanceCreate=interchainRuntime.EnqueueGovernanceCreateAndWait
 enqueueGovernanceApprove=interchainRuntime.EnqueueGovernanceApproveAndWait
 getProposalValidatorSet=evmInterchain.GetValidatorSet
 getProposalGovernanceNonce=evmInterchain.GetConfirmedILNGovernanceNonce
)

func getProposalCommand()*cobra.Command {
 cmd:=&cobra.Command{Use:"fee",Short:"Configure a single native validator fee per source chain"}
 cmd.AddCommand(getProposalCreateCommand(),getProposalApproveCommand(),getFeeExecuteCommand())
 return cmd
}
func getProposalCreateCommand()*cobra.Command {
 p:=&feeCreateParams{}
 cmd:=&cobra.Command{
 Use:"create",Short:"Propose source-chain validator fee without signing it",Args:cobra.NoArgs,
 PreRunE:func(_ *cobra.Command,_ []string)error{
 if p.dataDir==""||p.source==""{return fmt.Errorf("--data-dir and --source are required")}
 if _,err:=parsePositiveUint256(p.feeWei);err!=nil{return err}
 if p.ttl<=0||p.ttl>10*time.Minute{return fmt.Errorf("--ttl must be between 1s and 10m")}
 return nil
 },
 RunE:func(cmd *cobra.Command,_ []string)error{
  output:=command.InitializeOutputter(cmd);defer output.WriteOutput()
  result,err:=executeProposalCreate(p);if err!=nil{output.SetError(err);return nil}
  output.SetCommandResult(result);return nil
 },
 }
 cmd.Flags().StringVar(&p.dataDir,"data-dir","","running validator node data directory")
 cmd.Flags().StringVar(&p.source,"source","","configured source-chain name, e.g. base")
 cmd.Flags().StringVar(&p.feeWei,"fee-wei","","chain-wide validator fee in native smallest units")
 cmd.Flags().Uint64Var(&p.nonce,"nonce",0,"expected next source fee nonce; zero auto-detects")
 cmd.Flags().DurationVar(&p.ttl,"ttl",5*time.Minute,"proposal lifetime, max 10m")
 cmd.Flags().DurationVar(&p.timeout,"timeout",2*time.Minute,"worker response timeout")
 return cmd
}
func executeProposalCreate(p *feeCreateParams)(*FeeResult,error){
 source,err:=evmInterchain.Load(strings.ToLower(strings.TrimSpace(p.source)))
 if err!=nil{return nil,err}
 if err:=source.ValidateILNRead();err!=nil{return nil,err}
 if err:=source.ValidateMembershipRead();err!=nil{return nil,err}
 set,err:=getProposalValidatorSet(source);if err!=nil{return nil,err}
 current,err:=getProposalGovernanceNonce(source);if err!=nil{return nil,err}
 if current==^uint64(0){return nil,fmt.Errorf("source fee nonce exhausted")}
 next:=current+1
 if p.nonce!=0&&p.nonce!=next{return nil,fmt.Errorf("expected next source fee nonce %d",next)}
 fee,err:=parsePositiveUint256(p.feeWei);if err!=nil{return nil,err}
 proposal:=protocol.ILNSourceFeeProposal{
  SourceChainID:source.ChainID,SourceDomain:source.Domain,
  Registry:types.StringToAddress(source.ILNRegistryAddress),
  SetID:set.SetID,Nonce:next,ValidUntil:uint64(time.Now().Add(p.ttl).Unix()),
  ValidatorFeeWei:fee,
 }
 res,err:=enqueueGovernanceCreate(p.dataDir,proposal,p.timeout);if err!=nil{return nil,err}
 return &FeeResult{ProposalID:res.ProposalID,Quorum:res.Quorum},nil
}
func getProposalApproveCommand()*cobra.Command {
 p:=&feeApproveParams{}
 cmd:=&cobra.Command{
 Use:"approve",Short:"Explicitly sign a source fee proposal with this validator BLS key",Args:cobra.NoArgs,
 PreRunE:func(_ *cobra.Command,_ []string)error{
 if p.dataDir==""{return fmt.Errorf("--data-dir is required")}
 if _,err:=parseNonZeroHash(p.proposalID);err!=nil{return err}
 return nil
 },
 RunE:func(cmd *cobra.Command,_ []string)error{
 output:=command.InitializeOutputter(cmd);defer output.WriteOutput()
 r,err:=enqueueGovernanceApprove(p.dataDir,p.proposalID,p.timeout)
 if err!=nil{output.SetError(err);return nil}
 output.SetCommandResult(&FeeResult{ProposalID:r.ProposalID,Approved:r.Approved,Quorum:r.Quorum});return nil
 },
 }
 cmd.Flags().StringVar(&p.dataDir,"data-dir","","running validator node data directory")
 cmd.Flags().StringVar(&p.proposalID,"proposal-id","","source fee proposal hash")
 cmd.Flags().DurationVar(&p.timeout,"timeout",2*time.Minute,"worker response timeout")
 return cmd
}
func parsePositiveUint256(value string)(*big.Int,error){
 n,ok:=new(big.Int).SetString(strings.TrimSpace(value),10)
 if !ok||n.Sign()<=0||n.BitLen()>256{return nil,fmt.Errorf("--fee-wei must be positive uint256")}
 return n,nil
}
func parseNonZeroHash(value string)(types.Hash,error){
 value=strings.TrimSpace(value)
 if len(value)!=66||!strings.HasPrefix(value,"0x"){return types.ZeroHash,fmt.Errorf("invalid proposal ID")}
 raw,err:=hex.DecodeString(value[2:]);if err!=nil||len(raw)!=types.HashLength{return types.ZeroHash,fmt.Errorf("invalid proposal ID")}
 h:=types.BytesToHash(raw);if h==types.ZeroHash{return types.ZeroHash,fmt.Errorf("proposal ID must be nonzero")}
 return h,nil
}

// Execute submits an existing BLS fee quorum to the source-chain registry.
// It never creates a route and never signs on the operator's behalf.
func getFeeExecuteCommand() *cobra.Command {
	p:=&feeApproveParams{}
	cmd:=&cobra.Command{
		Use:"execute",Short:"Submit a completed source-chain fee quorum on chain",
		Args:cobra.NoArgs,
		PreRunE:func(_ *cobra.Command,_ []string)error{
			if strings.TrimSpace(p.dataDir)==""{return fmt.Errorf("--data-dir required")}
			if _,err:=parseNonZeroHash(p.proposalID);err!=nil{return err}
			if p.timeout<=0{return fmt.Errorf("--timeout must be positive")}
			return nil
		},
		RunE:func(cmd *cobra.Command,_ []string)error{
			out:=command.InitializeOutputter(cmd);defer out.WriteOutput()
			res,err:=interchainRuntime.EnqueueGovernanceExecuteAndWait(p.dataDir,strings.ToLower(p.proposalID),p.timeout)
			if err!=nil{out.SetError(err);return nil}
			out.SetCommandResult(&FeeExecuteResult{ProposalID:res.ProposalID,TxHash:res.TxHash,Nonce:res.Nonce})
			return nil
		},
	}
	cmd.Flags().StringVar(&p.dataDir,"data-dir","","local running validator data directory")
	cmd.Flags().StringVar(&p.proposalID,"proposal-id","","v3.1.5 source fee proposal")
	cmd.Flags().DurationVar(&p.timeout,"timeout",6*time.Minute,"on-chain execution timeout")
	return cmd
}
type FeeExecuteResult struct {
	ProposalID string `json:"proposalId"`
	TxHash string `json:"transactionHash"`
	Nonce uint64 `json:"nonce"`
}
func (r *FeeExecuteResult) GetOutput()string{
	return "\n[IBFT SOURCE FEE EXECUTED]\n"+helper.FormatKV([]string{
		fmt.Sprintf("Proposal ID|%s",r.ProposalID),
		fmt.Sprintf("Transaction hash|%s",r.TxHash),
		fmt.Sprintf("Confirmed chain-wide nonce|%d",r.Nonce),
	})
}
