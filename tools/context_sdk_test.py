"""Official SDK context/cache contracts; localhost fixture only, no model spend."""
from openai import OpenAI, BadRequestError

client = OpenAI(api_key="fixture-key", base_url="http://127.0.0.1:18787/v1",
                default_headers={"X-Fixture-Tenant": "python-context-sdk"}, max_retries=0, timeout=15)
items = []
for i in range(8):
    items.extend([{"role": "user", "content": f"Turn {i}: " + "historical detail " * 300},
                  {"role": "assistant", "content": "Keep src/main.go unchanged."}])

first = client.responses.compact(model="test-model", input=items)
raw = first.model_dump()
report = raw["x_oaiprism_context"]
assert raw["x_oaiprism_native_compaction"] is False
assert report["effective_input_tokens"] < report["original_input_tokens"]
assert report["summary_calls"] > 0
assert all(item.type != "compaction" for item in first.output), "Never fabricate provider-encrypted items"

second = client.responses.compact(model="test-model", input=items)
report2 = second.model_dump()["x_oaiprism_context"]
assert report2["summary_calls"] == 0 and report2["summary_cache_hits"] == 1
assert second.usage.input_tokens == 0 and second.usage.output_tokens == 0

next_input = [item.model_dump(exclude_none=True) for item in first.output]
next_input.append({"role": "user", "content": "Continue the next step."})
response = client.responses.create(model="test-model", input=next_input, store=False)
assert response.output_text == "Hello world"

with client.responses.stream(model="test-model", input=items, store=False,
        extra_body={"context_management": [{"type": "compaction", "compact_threshold": 1000}]}) as stream:
    list(stream)
    final = stream.get_final_response()
assert final.status == "completed"
auto = final.model_dump()["x_oaiprism_context"]
assert auto["effective_input_tokens"] < auto["original_input_tokens"]

raw_response = client.responses.with_raw_response.create(model="test-model", input="cache test", store=False,
        extra_body={"prompt_cache_key": "sdk-key", "prompt_cache_options": {"mode": "implicit", "ttl": "30m"}})
assert raw_response.headers["x-oaiprism-prompt-cache"] == "native-parameters-forwarded"
assert raw_response.parse().output_text == "Hello world"

try:
    client.responses.create(model="test-model", input="x", store=False,
                            extra_body={"prompt_cache_options": {"prewarm": True}})
except BadRequestError:
    pass
else:
    raise AssertionError("Unsupported cache prewarm was silently accepted")
client.close()
print("PASS: official Python compact/continuation, summary cache accounting, auto compaction and gated cache controls")
