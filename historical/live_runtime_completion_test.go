package historical

import (
 "context"
 "encoding/json"
 "fmt"
 "math/big"
 "os"
 "sync"
 "testing"
 "time"
 "ponder.local/ponder-go/checkpoint"
 "ponder.local/ponder-go/indexing"
 "ponder.local/ponder-go/schema"
 "ponder.local/ponder-go/store"
)

func TestRuntimeCompletionOrderingSharedReplay(t *testing.T){for _,backend:=range []string{"postgres","pglite"}{t.Run(backend,func(t *testing.T){
 root:=os.Getenv("PONDER_M45_TEST_ROOT");if root==""{t.Fatal("retained root required")};t.Setenv("PONDER_M44_TEST_ROOT",root);p:=newFiniteRPC(t,2);release:=make(chan struct{});var once sync.Once;calls:=[]uint64{};replaying:=false;replayCount:=0
 h:=indexing.Handler(func(ctx context.Context,a *indexing.HandlerArgs)error{
  id:=finiteChain(a);row,err:=a.Context.DB.Find(ctx,"account",store.Row{"id":"shared"});if err!=nil{return err};value:=big.NewInt(0);if row!=nil{var ok bool;value,ok=new(big.Int).SetString(fmt.Sprint(row["balance"]),10);if !ok{return fmt.Errorf("bad shared bigint")}}
  if replaying&&replayCount==0&&value.String()!="21"{return fmt.Errorf("global suffix was not reversed before replay: %s",value)}
  value.Mul(value,big.NewInt(10));value.Add(value,new(big.Int).SetUint64(id));if row==nil{_,err=a.Context.DB.InsertOne(ctx,"account",store.Row{"id":"shared","balance":value},store.Conflict{})}else{_,err=a.Context.DB.Update(ctx,"account",store.Row{"id":"shared"},store.Row{"balance":value})};if err!=nil{return err};calls=append(calls,id);if replaying{replayCount++};if id==2{once.Do(func(){close(release)})};return nil
 })
 b:=runtimeBuild(t,p,2,1,h);f:=newManagedFiniteFixture(t,backend,b);epochs:=[]uint64{};o:=RuntimeOptions{Historical:runtimeFixtureInput(f),BuildID:"completion-shared-v1",OpenID:"completion-1",Requesters:runtimeFactory(p,&epochs)};r:=runtimeOpen(t,backend,o)
 p.setHook(func(ctx context.Context,call finiteRPCRequest)error{if call.Chain==1&&call.Method=="eth_getBlockByNumber"&&len(call.Params)>1&&call.Params[0]=="0x1"&&call.Params[1]==true{select{case <-release:case <-ctx.Done():return ctx.Err()}};return nil})
 timeout:=30*time.Second;if backend=="pglite"{timeout=2*time.Minute};ctx,cancel:=context.WithTimeout(context.Background(),timeout);out,err:=r.Run(ctx);cancel();if err!=nil||!out.Completed||!out.Ready{t.Fatalf("completion source stalled behind slower chain: %+v err=%v",out,err)};if len(calls)!=4||calls[0]!=2||calls[1]!=1{t.Fatalf("default multichain imposed event order: %v",calls)};if err=r.Close(context.Background());err!=nil{t.Fatal(err)}
 inspect:=func(expected []uint64){t.Helper();managedInspect(t,f,func(db store.Backend){value:=big.NewInt(0);for _,id:=range expected{value.Mul(value,big.NewInt(10));value.Add(value,new(big.Int).SetUint64(id))};rows:=managedRows(t,db,"SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account WHERE id='shared'");if len(rows)!=1||rows[0]["balance"]!=value.String(){t.Fatalf("business execution differs from actual completion order %v rows=%v",expected,rows)};segments:=managedRows(t,db,"SELECT payload::text AS payload FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".\"_ponder_go_live_segments\" ORDER BY ordinal");if len(segments)!=2{t.Fatalf("unsafe suffix=%v",segments)};for i,row:=range segments{var segment store.ManagedLiveSegment;if err:=json.Unmarshal([]byte(fmt.Sprint(row["payload"])),&segment);err!=nil{t.Fatal(err)};cp,err:=checkpoint.Decode(segment.Block.Head.Checkpoint);if err!=nil||cp.ChainID!=expected[len(expected)-2+i]{t.Fatalf("ordinal suffix differs from actual handler order %v: %+v %v",expected,cp,err)}}})}
 inspect(calls);replaying=true;o.OpenID="completion-2";r=runtimeOpen(t,backend,o);out,err=r.Run(context.Background());if err!=nil||!out.Completed||!out.Ready||!out.Recovered||replayCount!=2{t.Fatalf("shared replay %+v calls=%d err=%v",out,replayCount,err)};if err=r.Close(context.Background());err!=nil{t.Fatal(err)};inspect(append([]uint64{2,1},calls[4:]...))
 })}}
