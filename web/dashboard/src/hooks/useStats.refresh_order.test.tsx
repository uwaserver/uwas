import { it, expect, vi, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/react';
import { useStats } from '@/hooks/useStats';
import type { StatsData } from '@/lib/api';
const mocks=vi.hoisted(()=>({stats:vi.fn(),health:vi.fn(),url:vi.fn()}));
vi.mock('@/lib/api',()=>({fetchStats:mocks.stats,fetchHealth:mocks.health,sseStatsURL:mocks.url}));
function deferred<T>() { let resolve!: (value:T)=>void;let reject!: (err:Error)=>void;const promise=new Promise<T>((r,j)=>{resolve=r;reject=j});return {promise,resolve,reject}; }
const stats=(requests_total:number)=>({ requests_total,cache_hits:0 } as StatsData);
afterEach(()=>{cleanup();vi.resetAllMocks()});
async function stale(olderFails=false,newerFails=false) {
 mocks.url.mockReturnValue(new Promise(()=>{}));mocks.health.mockResolvedValue({status:'healthy',uptime:'fixture'});
 const old=deferred<StatsData>();mocks.stats.mockReturnValueOnce(old.promise).mockResolvedValueOnce(stats(20));
 if(newerFails) mocks.stats.mockReset().mockReturnValueOnce(old.promise).mockRejectedValueOnce(new Error('newer failure'));
 const hook=renderHook(()=>useStats());let pending!:Promise<void>;
 act(()=>{pending=hook.result.current.refresh()});
 await act(async()=>{await hook.result.current.refresh()});
 const control={count:hook.result.current.stats?.requests_total,error:hook.result.current.error};
 await act(async()=>{if(olderFails) old.reject(new Error('older failure'));else old.resolve(stats(10));await pending});
 const actual={count:hook.result.current.stats?.requests_total,error:hook.result.current.error};hook.unmount();return {control,actual};
}
it('overlapping refreshes preserve newer completions and allow results while a newer request is pending',async()=>{
 expect((await stale()).actual).toEqual({count:20,error:null});
 expect((await stale(true)).actual).toEqual({count:20,error:null});
 expect((await stale(false,true)).actual).toEqual({count:undefined,error:'newer failure'});
 // An older completion is still useful while the newer request is pending.
 mocks.stats.mockReset();mocks.health.mockResolvedValue({status:'healthy',uptime:'fixture'});mocks.url.mockReturnValue(new Promise(()=>{}));
 const old=deferred<StatsData>(),newer=deferred<StatsData>();mocks.stats.mockReturnValueOnce(old.promise).mockReturnValueOnce(newer.promise);
 const hook=renderHook(()=>useStats());let a!:Promise<void>,b!:Promise<void>;
 act(()=>{a=hook.result.current.refresh();b=hook.result.current.refresh()});
 await act(async()=>{old.resolve(stats(10));await a});expect(hook.result.current.stats?.requests_total).toBe(10);
 await act(async()=>{newer.resolve(stats(20));await b});expect(hook.result.current.stats?.requests_total).toBe(20);hook.unmount();
});
