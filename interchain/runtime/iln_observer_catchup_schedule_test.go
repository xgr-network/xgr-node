package runtime

import (
 "testing"
 "time"

 evm "github.com/xgr-network/xgr-node/interchain/evm"
 "github.com/stretchr/testify/require"
)

func TestExternalObserverCatchupOnlyForKnownLaggingSource(t *testing.T) {
 base:=&evm.CheckpointRoute{Name:"base-behind",SourceNetwork:"base"}
 baseSibling:=&evm.CheckpointRoute{Name:"base-current",SourceNetwork:"base"}
 arb:=&evm.CheckpointRoute{Name:"arb-current",SourceNetwork:"arbitrum"}
 xgr:=&evm.CheckpointRoute{Name:"xgr-behind",SourceNetwork:"xgr"}
 w:=&Worker{
  destinations:map[string]*evm.Destination{
   "base":{ChainID:8453},"arbitrum":{ChainID:42161},"xgr":{ChainID:1643},
  },
  routes:map[string]*evm.CheckpointRoute{
   base.Name:base,baseSibling.Name:baseSibling,arb.Name:arb,xgr.Name:xgr,
  },
  ilnRouteHealthByName:map[string]ilnRouteHealth{
   base.Name:{Source:"base",SourceHeadKnown:true,ScanCursorKnown:true,BlockLag:10000,State:"catching-up"},
   baseSibling.Name:{Source:"base",SourceHeadKnown:true,ScanCursorKnown:true,BlockLag:0,State:"ok"},
   arb.Name:{Source:"arbitrum",SourceHeadKnown:true,ScanCursorKnown:true,BlockLag:0,State:"ok"},
   xgr.Name:{Source:"xgr",SourceHeadKnown:true,ScanCursorKnown:true,BlockLag:10000,State:"catching-up"},
  },
 }
 require.True(t,w.hasILNExternalObserverBacklog())
 require.True(t,w.ilnSourceHasExternalObserverBacklog("base"))
 require.False(t,w.ilnSourceHasExternalObserverBacklog("arbitrum"))
 require.False(t,w.ilnSourceHasExternalObserverBacklog("xgr"))
 require.False(t,w.ilnSourceHasExternalObserverBacklog("unknown"))

 // A transient transport error must not erase the previously verified lag.
 w.ilnRouteHealthByName[base.Name]=ilnRouteHealth{
  Source:"base",SourceHeadKnown:true,ScanCursorKnown:true,BlockLag:10000,State:"unavailable",
 }
 require.True(t,w.ilnSourceHasExternalObserverBacklog("base"))

 // The first confirmed fully caught-up scan returns to the quiet schedule.
 w.ilnRouteHealthByName[base.Name]=ilnRouteHealth{
  Source:"base",SourceHeadKnown:true,ScanCursorKnown:true,BlockLag:0,State:"ok",
 }
 require.False(t,w.hasILNExternalObserverBacklog())
}

func TestExternalObserverCatchupIntervalAndIdleCadence(t *testing.T) {
 now:=time.Unix(1000,0)
 var full,lastCatchup time.Time
 require.True(t,dueILNPeriodicPolling(now,&full,ilnExternalCheckpointInterval))
 lastCatchup=now // full scan also stamps the catch-up scheduler
 require.False(t,dueILNPeriodicPolling(now.Add(30*time.Second),&lastCatchup,ilnExternalCatchupInterval))
 require.True(t,dueILNPeriodicPolling(now.Add(60*time.Second),&lastCatchup,ilnExternalCatchupInterval))
 require.False(t,dueILNPeriodicPolling(now.Add(61*time.Second),&lastCatchup,ilnExternalCatchupInterval))
 require.False(t,dueILNPeriodicPolling(now.Add(9*time.Minute),&full,ilnExternalCheckpointInterval))
 require.True(t,dueILNPeriodicPolling(now.Add(10*time.Minute),&full,ilnExternalCheckpointInterval))
}
