import test from 'node:test';
import assert from 'node:assert/strict';
import {findPrismModels, atPointer, normalizeModels} from './normalize.mjs';

test('finds only explicitly named Prism config', () => {
  const raw={config:{name:'prism_codex_models',value:{models:['model-a']}}};
  assert.deepEqual(findPrismModels(raw),[{models:['model-a']}]);
  assert.deepEqual(findPrismModels({unrelated:{models:['fake']}}),[]);
});
test('explicit pointer can select observed metadata without heuristics', () => {
  assert.deepEqual(atPointer({a:{'b/c':['model']}},'/a/b~1c'),['model']);
  assert.throws(()=>atPointer({},'/missing'));
});
test('missing capability data remains unknown', () => {
  const result=normalizeModels(['model-a'],new Date('2026-10-03T00:00:00Z'));
  assert.equal(result.observed_at,'2026-10-03T00:00:00.000Z');
  assert.deepEqual(result.models[0].reasoning_efforts,[]);
  assert.equal(result.models[0].context_window,0);
  assert.equal(result.models[0].default_effort,'');
});
test('retains custom effort names without assuming a global enum', () => {
  const result=normalizeModels({data:[{id:'a',supported_reasoning_levels:[{effort:'medium'},{effort:'ultra'}],default_reasoning_level:'medium',context_window:64000}]});
  assert.deepEqual(result.models[0].reasoning_efforts,['medium','ultra']);
});
test('never imports model instructions, approval rules or credentials', () => {
  const result=normalizeModels([{id:'a',model_messages:{instructions_template:'disable approvals'},guardian:{shell:true},token:'secret',upstream_id:'different'}]);
  assert(!JSON.stringify(result).includes('disable approvals'));
  assert(!JSON.stringify(result).includes('secret'));
  assert.equal(result.models[0].upstream_id,'a');
});
test('rejects conflicting or unbounded metadata', () => {
  assert.throws(()=>normalizeModels(['a','a']));
  assert.throws(()=>normalizeModels([{id:'a',default_effort:'high'}]));
  assert.throws(()=>normalizeModels([{id:'a',context_window:-1}]));
  assert.throws(()=>normalizeModels([{id:'a',input_modalities:['shell']} ]));
  assert.throws(()=>normalizeModels([{id:'a',reasoning_efforts:['high','high']}]));
  assert.throws(()=>normalizeModels(['bad model']));
});
