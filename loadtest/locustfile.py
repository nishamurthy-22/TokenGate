"""Locust workloads for TokenGate.

Three simulated applications send streamed chat requests:
  support-bot   short replies, frequent
  summarizer    medium replies
  heavy-batch   long replies, sent back to back

Choose a workload with the PROFILE environment variable:
  steady        mostly light traffic (default)
  heavy_tenant  heavy-batch dominates, to test fairness
  bursty        users jump from 20 to 100 for 10 s every 30 s

Besides Locust's normal POST rows (time until response headers), every
request records two extra rows:
  TTFT  time to the first token
  FULL  time until the last token (successful requests only)
"""

import json
import os
import time

from locust import HttpUser, LoadTestShape, between, events, task

PROFILE = os.getenv("PROFILE", "steady")
PROMPT = "Please answer this question about distributed systems in detail. " * 4

# Relative number of users for (support-bot, summarizer, heavy-batch).
WEIGHTS = {
    "steady": (6, 3, 1),
    "heavy_tenant": (2, 2, 6),
    "bursty": (6, 3, 1),
}
if PROFILE not in WEIGHTS:
    raise SystemExit(f"Unknown PROFILE {PROFILE!r}; use one of {sorted(WEIGHTS)}")
W_SUPPORT, W_SUMMARY, W_HEAVY = WEIGHTS[PROFILE]


class TenantUser(HttpUser):
    abstract = True
    app_id = "support-bot"
    max_tokens = 32
    wait_time = between(0.5, 1.5)

    @task
    def chat(self):
        body = {
            "messages": [{"role": "user", "content": PROMPT}],
            "max_tokens": self.max_tokens,
            "stream": True,
        }
        headers = {"X-App-ID": self.app_id}
        start = time.perf_counter()
        first_token = None
        tokens = 0

        with self.client.post(
            "/v1/chat/completions",
            json=body,
            headers=headers,
            stream=True,
            catch_response=True,
            name=self.app_id,
        ) as resp:
            if resp.status_code != 200:
                try:
                    reason = resp.json().get("error", "")
                except ValueError:
                    reason = ""
                resp.failure(f"{resp.status_code} {reason}".strip())
                return

            error = None
            try:
                for line in resp.iter_lines():
                    if not line or not line.startswith(b"data:"):
                        continue
                    event = json.loads(line[len(b"data:"):])
                    if event.get("done"):
                        error = event.get("error")
                        break
                    if first_token is None:
                        first_token = time.perf_counter()
                    tokens += 1
            except Exception as exc:  # broken connection, bad JSON
                resp.failure(f"stream broken: {exc}")
                return

            if error:
                resp.failure(f"stream {error}")
            else:
                resp.success()

        if first_token is not None:
            self._record("TTFT", (first_token - start) * 1000, tokens)
        if not error:
            self._record("FULL", (time.perf_counter() - start) * 1000, tokens)

    def _record(self, kind, millis, tokens):
        events.request.fire(
            request_type=kind,
            name=self.app_id,
            response_time=millis,
            response_length=tokens,
            exception=None,
            context={},
        )


class SupportBot(TenantUser):
    weight = W_SUPPORT
    app_id = "support-bot"
    max_tokens = 32


class Summarizer(TenantUser):
    weight = W_SUMMARY
    app_id = "summarizer"
    max_tokens = 128


class HeavyBatch(TenantUser):
    weight = W_HEAVY
    app_id = "heavy-batch"
    max_tokens = 512
    wait_time = between(0.1, 0.5)


if PROFILE == "bursty":

    class BurstShape(LoadTestShape):
        """20 users, jumping to 100 users for 10 s out of every 30 s."""

        duration = float(os.getenv("DURATION_SECONDS", "180"))

        def tick(self):
            t = self.get_run_time()
            if t > self.duration:
                return None
            if t % 30 >= 20:
                return (100, 100)
            return (20, 50)
