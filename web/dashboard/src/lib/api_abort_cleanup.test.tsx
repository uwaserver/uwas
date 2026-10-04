import { it, expect, vi, afterEach } from 'vitest';
import { deployAppLive } from '@/lib/api';
const response = () => ({ ok: true, headers: new Headers({'Content-Type':'application/json'}), json: async () => ({ success: true, log: '' }) });
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });
async function completed() {
 const owner = new AbortController();
 const add = vi.spyOn(owner.signal, 'addEventListener');
 const remove = vi.spyOn(owner.signal, 'removeEventListener');
 let requestSignal: AbortSignal | undefined;
 vi.stubGlobal('fetch', vi.fn(async (_url: string, options: RequestInit) => { requestSignal = options.signal as AbortSignal; return response(); }));
 await deployAppLive('fixture', {}, () => {}, owner.signal);
 const listeners = add.mock.calls.filter(c => c[0] === 'abort').length - remove.mock.calls.filter(c => c[0] === 'abort').length;
 owner.abort();
 return { listeners, abortedAfterCompletion: requestSignal?.aborted };
}
it('live deploy detaches completed and failed abort listeners while preserving in-flight cancellation', async () => {
 for(let i=0;i<10;i++) expect(await completed()).toEqual({listeners:0,abortedAfterCompletion:false});
 const owner=new AbortController();const remove=vi.spyOn(owner.signal,'removeEventListener');
 vi.stubGlobal('fetch',vi.fn(async()=>{throw new Error('injected fetch failure')}));
 await expect(deployAppLive('fixture',{},()=>{},owner.signal)).rejects.toThrow('injected fetch failure');expect(remove).toHaveBeenCalledTimes(1);
 const active=new AbortController();const activeRemove=vi.spyOn(active.signal,'removeEventListener');
 let captured!: AbortSignal;let release!: () => void;
 vi.stubGlobal('fetch',vi.fn((_url:string,options:RequestInit)=>{captured=options.signal as AbortSignal;return new Promise((_resolve,reject)=>{release=()=>reject(new DOMException('cancelled','AbortError'));});}));
 const pending=deployAppLive('fixture',{},()=>{},active.signal);const failed=expect(pending).rejects.toThrow('cancelled');
 active.abort();expect(captured.aborted).toBe(true);release();await failed;expect(activeRemove).toHaveBeenCalledTimes(1);

});
