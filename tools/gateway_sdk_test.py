"""Official SDK contract tests against the local fixture. No paid upstream calls."""
import json
import unittest

from openai import OpenAI, APIStatusError, BadRequestError, NotFoundError
from pydantic import BaseModel

TOOL = {"type": "function", "name": "weather", "description": "Get weather",
        "parameters": {"type": "object", "properties": {"city": {"type": "string"}},
                       "required": ["city"], "additionalProperties": False}, "strict": True}

class Answer(BaseModel):
    n: int

class GatewaySDK(unittest.TestCase):
    def setUp(self):
        self.client = OpenAI(api_key="fixture-key", base_url="http://127.0.0.1:18787/v1",
                             default_headers={"X-Fixture-Tenant": "python-sdk"}, max_retries=0, timeout=10)
    def tearDown(self):
        self.client.close()

    def test_models_and_chat(self):
        self.assertIn("test-model", [model.id for model in self.client.models.list().data])
        response = self.client.chat.completions.create(model="test-model", messages=[{"role": "user", "content": "hello"}])
        self.assertEqual(response.choices[0].message.content, "Hello world")
        self.assertEqual(response.choices[0].finish_reason, "stop")
        self.assertEqual(response.usage.total_tokens, 16)

    def test_chat_stream_usage(self):
        chunks = list(self.client.chat.completions.create(model="test-model", messages=[{"role": "user", "content": "hello"}],
                      stream=True, stream_options={"include_usage": True}))
        self.assertEqual("".join(c.choices[0].delta.content or "" for c in chunks if c.choices), "Hello world")
        self.assertEqual(chunks[-1].choices, [])
        self.assertEqual(chunks[-1].usage.total_tokens, 16)
        self.assertEqual(len({c.id for c in chunks}), 1)

    def test_responses_accumulator(self):
        with self.client.responses.stream(model="test-model", input="hello", store=False) as stream:
            deltas = [event.delta for event in stream if event.type == "response.output_text.delta"]
            response = stream.get_final_response()
        self.assertEqual("".join(deltas), "Hello world")
        self.assertEqual(response.output_text, "Hello world")
        self.assertEqual(response.status, "completed")

    def test_structured_parse(self):
        response = self.client.responses.parse(model="test-model", input="a number", text_format=Answer, store=False)
        self.assertEqual(response.output_parsed.n, 3)

    def test_response_tool_stream_and_followup(self):
        with self.client.responses.stream(model="test-model", input="weather?", tools=[TOOL], tool_choice="required", store=True) as stream:
            list(stream)
            response = stream.get_final_response()
        call = next(item for item in response.output if item.type == "function_call")
        self.assertEqual(call.name, "weather")
        self.assertEqual(json.loads(call.arguments), {"city": "London"})
        followup = self.client.responses.create(model="test-model", previous_response_id=response.id,
                    input=[{"type": "function_call_output", "call_id": call.call_id, "output": "sunny"}],
                    tools=[TOOL], tool_choice="auto", store=False)
        self.assertEqual(followup.output_text, "The weather is sunny.")
        stored = self.client.responses.retrieve(response.id)
        self.assertEqual(stored.id, response.id)
        self.assertTrue(self.client.responses.delete(response.id).deleted)
        with self.assertRaises(NotFoundError):
            self.client.responses.retrieve(response.id)

    def test_chat_tool_roundtrip(self):
        tool = {"type": "function", "function": {k: v for k, v in TOOL.items() if k != "type"}}
        first = self.client.chat.completions.create(model="test-model", messages=[{"role": "user", "content": "weather?"}],
                    tools=[tool], tool_choice="required")
        self.assertEqual(first.choices[0].finish_reason, "tool_calls")
        message = first.choices[0].message
        call = message.tool_calls[0]
        followup = self.client.chat.completions.create(model="test-model", messages=[
            {"role": "user", "content": "weather?"},
            {"role": "assistant", "content": None, "tool_calls": [{"id": call.id, "type": "function",
                  "function": {"name": call.function.name, "arguments": call.function.arguments}}]},
            {"role": "tool", "tool_call_id": call.id, "content": "sunny"}], tools=[tool])
        self.assertEqual(followup.choices[0].message.content, "The weather is sunny.")

    def test_failures_are_not_success(self):
        with self.assertRaises(APIStatusError) as error:
            self.client.responses.create(model="failure", input="x", stream=True)
        self.assertEqual(error.exception.status_code, 429)
        events = list(self.client.responses.create(model="partial-failure", input="x", stream=True, store=False))
        self.assertEqual(events[-1].type, "response.failed")
        self.assertNotIn("response.completed", [event.type for event in events])
        with self.assertRaises(BadRequestError):
            self.client.responses.create(model="test-model", input="x", temperature=0)
        with self.assertRaises(APIStatusError):
            self.client.responses.create(model="bad-schema", input="x", text={"format": {
                "type": "json_schema", "name": "answer", "strict": True,
                "schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}}})

    def test_local_limit_incomplete(self):
        events = list(self.client.responses.create(model="test-model", input="hello", max_output_tokens=1,
                                                    stream=True, store=False))
        self.assertEqual(events[-1].type, "response.incomplete")
        self.assertEqual(events[-1].response.incomplete_details.reason, "max_output_tokens")

if __name__ == "__main__":
    unittest.main(verbosity=2)
