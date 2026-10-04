import assert from 'node:assert/strict';
import OpenAI from 'openai';

const client = new OpenAI({apiKey: 'fixture-key', baseURL: 'http://127.0.0.1:18787/v1',
  defaultHeaders: {'X-Fixture-Tenant': 'javascript-context-sdk'}, maxRetries: 0, timeout: 15000});
const input = [];
for (let i = 0; i < 8; i++) {
  input.push({role: 'user', content: `Turn ${i}: ` + 'historical detail '.repeat(300)});
  input.push({role: 'assistant', content: 'Keep src/main.go unchanged.'});
}
const compacted = await client.responses.compact({model: 'test-model', input});
assert.equal(compacted.x_oaiprism_native_compaction, false);
assert(compacted.x_oaiprism_context.effective_input_tokens < compacted.x_oaiprism_context.original_input_tokens);
assert(compacted.output.every(item => item.type !== 'compaction'));
const repeated = await client.responses.compact({model: 'test-model', input});
assert.equal(repeated.x_oaiprism_context.summary_calls, 0);
assert.equal(repeated.x_oaiprism_context.summary_cache_hits, 1);
assert.equal(repeated.usage.input_tokens, 0);
const next = await client.responses.create({model: 'test-model', store: false,
  input: [...compacted.output, {role: 'user', content: 'Continue.'}]});
assert.equal(next.output_text, 'Hello world');
const stream = client.responses.stream({model: 'test-model', input, store: false,
  context_management: [{type: 'compaction', compact_threshold: 1000}]});
const final = await stream.finalResponse();
assert.equal(final.status, 'completed');
assert(final.x_oaiprism_context.effective_input_tokens < final.x_oaiprism_context.original_input_tokens);
const {response} = await client.responses.create({model: 'test-model', input: 'cache', store: false,
  prompt_cache_key: 'js-sdk', prompt_cache_options: {mode: 'implicit', ttl: '30m'}}).withResponse();
assert.equal(response.headers.get('x-oaiprism-prompt-cache'), 'native-parameters-forwarded');
console.log('PASS: official JavaScript compact, cached summary reuse, continuation, automatic compression and cache forwarding');
