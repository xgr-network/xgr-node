package interchain

import (
 "fmt"
 "io/fs"
 "os"
 "path/filepath"
 "strings"

 "github.com/spf13/cobra"
 "github.com/xgr-network/xgr-node/command"
 "github.com/xgr-network/xgr-node/command/helper"
)

type ILNStorageStatusResult struct {
 BaseDir string `json:"baseDir"`
 TotalFiles uint64 `json:"totalFiles"`
 TotalBytes uint64 `json:"totalBytes"`
 AttestationFiles uint64 `json:"attestationFiles"`
 LocalVoteFiles uint64 `json:"localVoteFiles"`
 RequestFiles uint64 `json:"requestFiles"`
 GovernanceFiles uint64 `json:"governanceFiles"`
 OtherFiles uint64 `json:"otherFiles"`
}
func (r *ILNStorageStatusResult) GetOutput() string {
 return "\n[IBFT INTERCHAIN ILN STORAGE]\n"+helper.FormatKV([]string{
  fmt.Sprintf("Data directory|%s",r.BaseDir),
  fmt.Sprintf("ILN files|%d",r.TotalFiles),
  fmt.Sprintf("Total ILN bytes|%d",r.TotalBytes),
  fmt.Sprintf("Transfer quorum archive files|%d",r.AttestationFiles),
  fmt.Sprintf("Local ILN vote files|%d",r.LocalVoteFiles),
  fmt.Sprintf("Public quorum request files|%d",r.RequestFiles),
  fmt.Sprintf("Governance files|%d",r.GovernanceFiles),
  fmt.Sprintf("Other ILN state files|%d",r.OtherFiles),
 })
}

// This is a read-only offline diagnostics command; it never modifies the
// validator's storage and does not require a running worker or wallet keys.
func getILNStorageStatusCommand() *cobra.Command {
 var dataDir string
 cmd:=&cobra.Command{
  Use:"storage-status",
  Short:"Measure persisted ILN quorum, vote, request and governance disk usage",
  Args:cobra.NoArgs,
  PreRunE:func(_ *cobra.Command,_ []string)error{
   if strings.TrimSpace(dataDir)==""{return fmt.Errorf("--data-dir is required")}
   return nil
  },
  RunE:func(cmd *cobra.Command,_ []string)error{
   out:=command.InitializeOutputter(cmd)
   defer out.WriteOutput()
   r,err:=readILNStorageStatus(dataDir)
   if err!=nil{out.SetError(err);return nil}
   out.SetCommandResult(r)
   return nil
  },
 }
 cmd.Flags().StringVar(&dataDir,"data-dir","","validator node data directory")
 return cmd
}

func readILNStorageStatus(dataDir string)(*ILNStorageStatusResult,error){
 result:=&ILNStorageStatusResult{BaseDir:dataDir}
 root:=filepath.Join(dataDir,"interchain")
 if _,err:=os.Stat(root);err!=nil{return nil,err}
 err:=filepath.WalkDir(root,func(path string,entry fs.DirEntry,walkErr error)error{
  if walkErr!=nil{return walkErr}
  if entry.IsDir(){return nil}
  if !entry.Type().IsRegular(){return nil}
  info,err:=entry.Info()
  if err!=nil{return err}
  rel,err:=filepath.Rel(root,path)
  if err!=nil{return err}
  result.TotalFiles++
  result.TotalBytes+=uint64(info.Size())
  switch{
  case strings.HasPrefix(rel,"attestations"+string(filepath.Separator)):
   result.AttestationFiles++
  case strings.HasPrefix(rel,filepath.Join("iln","local-votes")+string(filepath.Separator)):
   result.LocalVoteFiles++
  case strings.HasPrefix(rel,filepath.Join("iln","quorum-requests")+string(filepath.Separator)):
   result.RequestFiles++
  case strings.HasPrefix(rel,"governance"+string(filepath.Separator)):
   result.GovernanceFiles++
  default:
   result.OtherFiles++
  }
  return nil
 })
 if err!=nil{return nil,err}
 return result,nil
}
