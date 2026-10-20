package runtime

import (
 "testing"
 "time"

 "github.com/stretchr/testify/require"
)

// Check the heavy remote RPC gate without requiring public Base access.
func TestV321ILNExternalPollingDoesNotFollowTwoSecondTick(t *testing.T){
 now:=time.Unix(1000,0)
 var last time.Time
 require.True(t,dueILNPeriodicPolling(now,&last,ilnExternalCheckpointInterval))
 for i:=1;i<15;i++{
  require.False(t,dueILNPeriodicPolling(now.Add(time.Duration(i)*2*time.Second),&last,ilnExternalCheckpointInterval))
 }
 require.True(t,dueILNPeriodicPolling(now.Add(30*time.Second),&last,ilnExternalCheckpointInterval))
}
func TestV321ILNExternalBackgroundRetryBackoff(t *testing.T){
 now:=time.Unix(1000,0)
 var membership,discovery,retry time.Time
 require.True(t,dueILNPeriodicPolling(now,&membership,ilnExternalMembershipInterval))
 require.True(t,dueILNPeriodicPolling(now,&discovery,ilnExternalDiscoveryInterval))
 require.True(t,dueILNPeriodicPolling(now,&retry,ilnExternalRetryInterval))
 for i:=1;i<15;i++{
  instant:=now.Add(time.Duration(i)*2*time.Second)
  require.False(t,dueILNPeriodicPolling(instant,&membership,ilnExternalMembershipInterval))
  require.False(t,dueILNPeriodicPolling(instant,&discovery,ilnExternalDiscoveryInterval))
  require.False(t,dueILNPeriodicPolling(instant,&retry,ilnExternalRetryInterval))
 }
 require.True(t,dueILNPeriodicPolling(now.Add(30*time.Second),&retry,ilnExternalRetryInterval))
 require.False(t,dueILNPeriodicPolling(now.Add(30*time.Second),&membership,ilnExternalMembershipInterval))
 require.True(t,dueILNPeriodicPolling(now.Add(60*time.Second),&membership,ilnExternalMembershipInterval))
 require.True(t,dueILNPeriodicPolling(now.Add(60*time.Second),&discovery,ilnExternalDiscoveryInterval))
}
