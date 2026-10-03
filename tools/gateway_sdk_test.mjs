import assert from 'node:assert/strict';
import OpenAI from 'openai';

const client = new OpenAI({apiKey: 'fixture-key', baseURL: 'http://127.0.0.1:18787/v1',
  defaultHeaders: {'X-Fixture-Tenant': 'javascript-sdk'}, maxRetries: 0, timeout: 10000});
const tool = {type: 'function', name: 'weather', description: 'Get weather',
  parameters: {type: 'object', properties: {city: {type: 'string'}}, required: ['city'], additionalProperties: false}, strict: true};

const chat = await client.chat.completions.create({model: 'test-model', messages: [{role: 'user', content: 'hello'}]});
assert.equal(chat.choices[0].message.content, 'Hello world');
assert.equal(chat.usage.total_tokens, 16);

const chatStream = await client.chat.completions.create({model: 'test-model', messages: [{role: 'user', content: 'hello'}],
  stream: true, stream_options: {include_usage: true}});
let text = '', last;
for await (const chunk of chatStream) { text += chunk.choices[0]?.delta?.content ?? ''; last = chunk; }
assert.equal(text, 'Hello world');
assert.deepEqual(last.choices, []);
assert.equal(last.usage.total_tokens, 16);

const responseStream = client.responses.stream({model: 'test-model', input: 'hello', store: false});
const final = await responseStream.finalResponse();
assert.equal(final.status, 'completed');
assert.equal(final.output_text, 'Hello world');

const toolStream = client.responses.stream({model: 'test-model', input: 'weather?', tools: [tool], tool_choice: 'required', store: true});
const first = await toolStream.finalResponse();
const call = first.output.find(item => item.type === 'function_call');
assert.equal(call.name, 'weather');
assert.deepEqual(JSON.parse(call.arguments), {city: 'London'});
const second = await client.responses.create({model: 'test-model', previous_response_id: first.id,
  input: [{type: 'function_call_output', call_id: call.call_id, output: 'sunny'}], tools: [tool], store: false});
assert.equal(second.output_text, 'The weather is sunny.');
assert.equal((await client.responses.retrieve(first.id)).id, first.id);
assert.equal((await client.responses.delete(first.id)).deleted, true);

const failed = await client.responses.create({model: 'partial-failure', input: 'hello', stream: true, store: false});
const types = [];
for await (const event of failed) types.push(event.type);
assert.equal(types.at(-1), 'response.failed');
assert(!types.includes('response.completed'));
await assert.rejects(client.responses.create({model: 'failure', input: 'x'}), error => error.status === 429);
console.log('PASS: JavaScript SDK chat, usage stream, Responses accumulation, tools, continuation, deletion and failure contracts');
