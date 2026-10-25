package runtime

import (
    "fmt"
    "strings"
    "time"

    evm "github.com/xgr-network/xgr-node/interchain/evm"
)

// All limits are compiled Interchain policy. Source finality and the two
// independent full-evidence witnesses remain in the EVM verification layer.
// These bounded scans provide discovery hints only.
const (
    directILNMaxWindowsPerPoll = 4
    directILNRemoteWorkBudget = uint64(2048)
    directILNNativeWorkBudget = uint64(4000)
    directILNRemoteMaxWindow = uint64(512)
    directILNNativeMaxWindow = uint64(1000)
    directILNScanWallBudget = 8 * time.Second
    directILNRetryMaximum = 4 * time.Minute
)

type directILNScanWindowReader func(cursor uint64, exists bool, window uint64) (
    head, from, to uint64, operations []evm.ILNOperation, err error,
)

type directILNScanRetry struct {
    until time.Time
    failures uint8
}

// directILNWindow expands the catch-up window according to the previously
// confirmed source head. The EVM/IBFT scanner rechecks its own finality head.
// Never trust a latest/unconfirmed head or extrapolate L1 block numbers.
func directILNWindow(lag uint64, native bool) uint64 {
    if native {
        if lag >= 2000 {return directILNNativeMaxWindow}
        if lag >= 512 {return 512}
        return 256
    }
    if lag >= 512 {return directILNRemoteMaxWindow}
    return 256
}

// directILNInitialCursor uses the exact receipt-pinned RPC-native source
// registry deployment block. Starting 128 blocks before "now" can permanently
// lose events from an older but newly discovered permissionless route.
func directILNInitialCursor(source *evm.Destination) (uint64,error) {
    if source==nil||source.ILNRegistryActivationBlock==0 {
        return 0,fmt.Errorf("verified ILN source registry deployment height required for historical scan")
    }
    return source.ILNRegistryActivationBlock-1,nil
}

// runDirectILNCatchup shares the durable cursor protocol between local IBFT
// and external confirmed-RPC scans. process MUST finish all full BLS security
// checks and persist any local votes before the window is committed.
// Each successful window is atomic from the cursor's perspective; failures
// replay the uncommitted window without silently skipping source events.
func runDirectILNCatchup(
    source *evm.Destination, cursor uint64, exists bool,
    read directILNScanWindowReader,
    process func([]evm.ILNOperation)error,
    commit func(uint64)error,
) (head, updatedCursor uint64, err error) {
    if source==nil||read==nil||process==nil||commit==nil {
        return 0,cursor,fmt.Errorf("missing Direct-BLS catch-up dependency")
    }
    if !exists {
        cursor,err=directILNInitialCursor(source)
        if err!=nil{return 0,0,err}
    }
    updatedCursor=cursor
    native:=source.ChainID==1643
    budget:=directILNRemoteWorkBudget
    if native{budget=directILNNativeWorkBudget}
    deadline:=time.Now().Add(directILNScanWallBudget)
    // The first request is deliberately small, so rate-limited providers
    // are not immediately flooded with maximum-sized eth_getLogs ranges.
    var lag uint64
    for attempt:=0;attempt<directILNMaxWindowsPerPoll&&budget>0;attempt++ {
        if attempt>0&&time.Now().After(deadline){break}
        window:=directILNWindow(lag,native)
        if window>budget{window=budget}
        var from,to uint64
        var operations []evm.ILNOperation
        var nextHead uint64
        nextHead,from,to,operations,err=read(updatedCursor,true,window)
        if err!=nil{return head,updatedCursor,err}
        head=nextHead
        if head<updatedCursor {
            return head,updatedCursor,fmt.Errorf("finality-qualified ILN head regressed behind durable cursor")
        }
        if from==0 {
            if to!=0||len(operations)!=0||head!=updatedCursor {
                return head,updatedCursor,fmt.Errorf("invalid empty ILN scan window")
            }
            break
        }
        if from!=updatedCursor+1||to<from||to>head||to-from+1>window {
            return head,updatedCursor,fmt.Errorf("invalid Direct-BLS scan range")
        }
        if err=process(operations);err!=nil{return head,updatedCursor,err}
        if err=commit(to);err!=nil{return head,updatedCursor,err}
        budget-=to-from+1
        updatedCursor=to
        if updatedCursor>=head{break}
        lag=head-updatedCursor
    }
    return head,updatedCursor,nil
}

func directILNTransportFailure(err error) bool {
    if err==nil{return false}
    s:=strings.ToLower(err.Error())
    for _,v:=range []string{
        "429","rate limit","rpc provider","rpc unavailable","cooldown",
        "timeout","deadline exceeded","connection","eof","502","503","504","403",
        "network is unreachable",
    } {
        if strings.Contains(s,v){return true}
    }
    return false
}

func directILNRetryDelay(failures uint8)time.Duration {
    if failures==0{return 0}
    if failures>4{failures=4}
    delay:=30*time.Second
    for n:=uint8(1);n<failures;n++{delay*=2}
    if delay>directILNRetryMaximum{return directILNRetryMaximum}
    return delay
}

func (w *Worker) directILNScanMayRun(route *evm.CheckpointRoute, now time.Time)bool{
    if w==nil||route==nil{return false}
    w.mu.Lock()
    defer w.mu.Unlock()
    for _,key:=range []string{"route:"+route.Name,"source:"+route.SourceNetwork} {
        if now.Before(w.ilnDirectScanRetries[key].until){return false}
    }
    return true
}

func (w *Worker) directILNScanFinished(route *evm.CheckpointRoute, now time.Time,err error){
    if w==nil||route==nil{return}
    w.mu.Lock()
    defer w.mu.Unlock()
    if w.ilnDirectScanRetries==nil {
        w.ilnDirectScanRetries=make(map[string]directILNScanRetry)
    }
    routeKey:="route:"+route.Name
    if err==nil{
        delete(w.ilnDirectScanRetries,routeKey)
        // Provider cooldown is tracked separately in interchain/evm.
        // Clear the source-level gate only after an actual successful read.
        delete(w.ilnDirectScanRetries,"source:"+route.SourceNetwork)
        return
    }
    key:=routeKey
    if directILNTransportFailure(err){key="source:"+route.SourceNetwork}
    state:=w.ilnDirectScanRetries[key]
    if state.failures<4{state.failures++}
    state.until=now.Add(directILNRetryDelay(state.failures))
    w.ilnDirectScanRetries[key]=state
}

// finishILNObserverScanBackoff applies a shared observer transport failure
// exactly once per source chain, not once for every route receiving the same
// failure from scanILNObserverRoutes. Route-specific failures retain separate
// retry gates. Process successes before shared failures, so one successful
// route cannot erroneously clear another route's source-level cooldown.
func (w *Worker) finishILNObserverScanBackoff(
 routes []*evm.CheckpointRoute, failures map[string]error, now time.Time,
) {
 type failedSource struct {
  route *evm.CheckpointRoute
  err error
 }
 sourceFailures:=make(map[string]failedSource)
 for _,route:=range routes {
  if route==nil {continue}
  err:=failures[route.Name]
  if err!=nil && directILNTransportFailure(err) {
   if _,seen:=sourceFailures[route.SourceNetwork];!seen {
    sourceFailures[route.SourceNetwork]=failedSource{route:route,err:err}
   }
   continue
  }
  w.directILNScanFinished(route,now,err)
 }
 for _,failure:=range sourceFailures {
  w.directILNScanFinished(failure.route,now,failure.err)
 }
}
