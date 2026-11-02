import { beforeEach, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import PHPConfig from '@/pages/PHPConfig';
import { ConfirmContext } from '@/components/useConfirm';
const api = vi.hoisted(() => ({fetchPHP:vi.fn(),fetchPHPConfig:vi.fn(),fetchPHPConfigRaw:vi.fn(),updatePHPConfigKey:vi.fn(),savePHPConfigRaw:vi.fn(),restartPHP:vi.fn()}));
vi.mock('@/lib/api',()=>api);
const flush=()=>act(async()=>{});
const confirm={confirmAction:vi.fn(async()=>true),promptText:vi.fn(async()=>null)};
beforeEach(()=>{
 Object.values(api).forEach(fn=>fn.mockReset());
 api.fetchPHP.mockResolvedValue([{version:'8.3.2',sapi:'fpm',disabled:false},{version:'8.2.1',sapi:'fpm',disabled:false}]);
 api.fetchPHPConfig.mockImplementation(async(v:string)=>({memory_limit:v==='8.3.2'?'512M':'96M'}));
 api.fetchPHPConfigRaw.mockResolvedValue({content:'; ini'});
 api.updatePHPConfigKey.mockResolvedValue({status:'ok'});
});
function mount(){render(<ConfirmContext.Provider value={confirm}><PHPConfig/></ConfirmContext.Provider>);}
it('control: current version accepts a completed preset',async()=>{
 mount();await flush();fireEvent.click(screen.getByRole('button',{name:/Laravel/}));await flush();
 expect(screen.getByDisplayValue('512M')).toBeInTheDocument();
 expect(screen.getByText(/Laravel preset applied/)).toBeInTheDocument();
 console.log('CONTROL EXPECTED: preset applied ACTUAL: preset applied');
});

for(const variant of ['success','failure','switch-back']) {
 it(`ignores stale preset ${variant}`,async()=>{
  let release!: (value:unknown)=>void;
  let reject!: (reason:Error)=>void;
  api.updatePHPConfigKey.mockImplementationOnce(()=>new Promise((resolve,fail)=>{release=resolve;reject=fail;}));
  mount();await flush();fireEvent.click(screen.getByRole('button',{name:/Laravel/}));await flush();
  expect(api.updatePHPConfigKey).toHaveBeenCalledTimes(1);
  fireEvent.change(screen.getByRole('combobox'),{target:{value:'8.2.1'}});await flush();
  expect(screen.getByDisplayValue('96M')).toBeInTheDocument();
  if(variant==='switch-back') {
   fireEvent.change(screen.getByRole('combobox'),{target:{value:'8.3.2'}});await flush();
  }
  await act(async()=>{if(variant==='failure')reject(new Error('old preset failed'));else release({status:'ok'});});await flush();
  expect(screen.getByDisplayValue(variant==='switch-back'?'512M':'96M')).toBeInTheDocument();
  expect(screen.queryByDisplayValue('120')).toBeNull();
  expect(screen.queryByText(/Laravel preset applied|old preset failed/)).toBeNull();
  expect(screen.getByRole('button',{name:/Laravel/})).not.toBeDisabled();
  // A fresh action after stale completion still succeeds for the selected version.
  fireEvent.click(screen.getByRole('button',{name:/WordPress/}));await flush();
  expect(screen.getByDisplayValue('256M')).toBeInTheDocument();
  expect(screen.getByText(/WordPress preset applied/)).toBeInTheDocument();
  console.log('FIX VERIFIED');
 });
}
